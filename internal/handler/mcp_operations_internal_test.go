package handler

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/access"
	"github.com/calnode/calnode/internal/db"
)

func agentFixture(t *testing.T) (*Handler, AuthUser, string) {
	t.Helper()
	d, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err = db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	h := New(d, slog.Default())
	h.baseURL = "http://localhost:39140"
	w := httptest.NewRecorder()
	h.Setup(w, httptest.NewRequest("POST", "/v1/setup", strings.NewReader(`{"name":"Owner","email":"owner@example.test","timezone":"UTC"}`)))
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	var setup struct {
		Key string `json:"api_key"`
	}
	if json.Unmarshal(w.Body.Bytes(), &setup) != nil {
		t.Fatal("setup")
	}
	u, _, err := h.mcpPrincipal(context.Background(), setup.Key)
	if err != nil {
		t.Fatal(err)
	}
	return h, u, setup.Key
}
func personalEventOp(slug string) agentOperation {
	return agentOperation{Name: "create_event_type", Values: map[string]any{"slug": slug, "name": "Imported", "duration_minutes": 30, "location_type": "phone", "location_value": "+1 555 123 4567"}}
}
func countAgentEvents(t *testing.T, h *Handler) int {
	t.Helper()
	var n int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM event_types`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func requestID(t *testing.T, v map[string]any) string {
	t.Helper()
	id, ok := v["id"].(string)
	if !ok {
		t.Fatalf("missing request id: %#v", v)
	}
	return id
}
func TestAgentReview_ExactBatchAndReplay(t *testing.T) {
	h, u, _ := agentFixture(t)
	ctx := context.Background()
	v, err := h.submitAgentChanges(ctx, u, []agentOperation{personalEventOp("import-a"), personalEventOp("import-b")})
	if err != nil {
		t.Fatal(err)
	}
	id := requestID(t, v)
	if v["status"] != "pending" || countAgentEvents(t, h) != 0 {
		t.Fatalf("executed before approval: %#v", v)
	}
	v, err = h.executeAgentChanges(ctx, id, u.ID, true)
	if err != nil || v["status"] != "completed" || countAgentEvents(t, h) != 2 {
		t.Fatalf("apply: %#v %v", v, err)
	}
	var active, public bool
	if err = h.db.QueryRow(`SELECT is_active,is_public FROM event_types WHERE slug='import-a'`).Scan(&active, &public); err != nil || active || public {
		t.Fatalf("import published: %v %v %v", active, public, err)
	}
	if _, err = h.executeAgentChanges(ctx, id, u.ID, true); err == nil {
		t.Fatal("replayed approval")
	}
}
func TestAgentReview_AgentCannotApprove(t *testing.T) {
	h, u, key := agentFixture(t)
	v, err := h.submitAgentChanges(context.Background(), u, []agentOperation{personalEventOp("no-self-approval")})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/v1/mcp/change-requests/x/decision", strings.NewReader(`{"approve":true}`))
	r.SetPathValue("id", requestID(t, v))
	r.Header.Set("X-API-Key", key)
	w := httptest.NewRecorder()
	h.RequireAuth(h.DecideMCPChangeRequest)(w, r)
	if w.Code != 403 || countAgentEvents(t, h) != 0 {
		t.Fatalf("credential approved itself: %d %s", w.Code, w.Body.String())
	}
}
func TestAgentReview_RevocationExpiryAndPermissionChanges(t *testing.T) {
	for _, mode := range []string{"revoked", "expired", "narrowed", "wrong-user"} {
		t.Run(mode, func(t *testing.T) {
			h, u, _ := agentFixture(t)
			v, err := h.submitAgentChanges(context.Background(), u, []agentOperation{personalEventOp("blocked")})
			if err != nil {
				t.Fatal(err)
			}
			id := requestID(t, v)
			user := u.ID
			switch mode {
			case "revoked":
				_, err = h.db.Exec(`DELETE FROM api_keys WHERE id=?`, u.CredentialID)
			case "expired":
				_, err = h.db.Exec(`UPDATE mcp_change_requests SET expires_at='2000-01-01T00:00:00Z' WHERE id=?`, id)
			case "narrowed":
				_, err = h.db.Exec(`UPDATE api_keys SET permissions_json='{"mcp.connect":"own"}' WHERE id=?`, u.CredentialID)
			case "wrong-user":
				user = "someone-else"
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = h.executeAgentChanges(context.Background(), id, user, true); err == nil || countAgentEvents(t, h) != 0 {
				t.Fatalf("%s not enforced: %v", mode, err)
			}
		})
	}
}
func TestAgentReview_StaleStateAndCompanyPolicy(t *testing.T) {
	h, u, _ := agentFixture(t)
	ctx := context.Background()
	_, _, err := h.callAgentOperation(ctx, u, personalEventOp("stale"))
	if err != nil {
		t.Fatal(err)
	}
	op := agentOperation{Name: "update_event_type", Target: "stale", Values: map[string]any{"name": "Agent edit"}}
	v, err := h.submitAgentChanges(ctx, u, []agentOperation{op})
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.db.Exec(`UPDATE event_types SET name='Human edit' WHERE slug='stale'`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.executeAgentChanges(ctx, requestID(t, v), u.ID, true); err == nil {
		t.Fatal("approved stale state")
	}
	_, err = h.db.Exec(`UPDATE users SET mcp_review_required=0 WHERE id=?`, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	v, err = h.submitAgentChanges(ctx, u, []agentOperation{personalEventOp("automatic")})
	if err != nil || v["status"] != "completed" {
		t.Fatalf("preference: %#v %v", v, err)
	}
	_, err = h.db.Exec(`UPDATE server_settings SET signin_policy_json='{"mcp_review_enforced":true}' WHERE id=1`)
	if err != nil {
		t.Fatal(err)
	}
	v, err = h.submitAgentChanges(ctx, u, []agentOperation{personalEventOp("enforced")})
	if err != nil || v["status"] != "pending" {
		t.Fatalf("company override: %#v %v", v, err)
	}
}
func TestAgentReview_RejectSecretsAndRedactReads(t *testing.T) {
	h, u, _ := agentFixture(t)
	for _, field := range []string{"client_secret", "smtp_pass", "access_token", "refresh_token", "private_key", "password"} {
		_, err := h.submitAgentChanges(context.Background(), u, []agentOperation{{Name: "draft_identity_provider", Values: map[string]any{"kind": "microsoft", field: "do-not-store"}}})
		if err == nil {
			t.Errorf("accepted secret %s", field)
		}
	}
	var requests int
	_ = h.db.QueryRow(`SELECT COUNT(*) FROM mcp_change_requests`).Scan(&requests)
	if requests != 0 {
		t.Fatal("stored rejected secret")
	}
	v := map[string]any{"client_secret": "secret", "client_secret_set": true, "nested": map[string]any{"refresh_token": "secret"}, "details": "old secret-bearing audit data"}
	raw, _ := json.Marshal(redactAgentData(v))
	if strings.Contains(string(raw), `"secret"`) || !strings.Contains(string(raw), `"client_secret_set":true`) {
		t.Fatal(string(raw))
	}
}
func TestAgentReview_PartialBatchStopsWithoutReplay(t *testing.T) {
	h, u, _ := agentFixture(t)
	ops := []agentOperation{personalEventOp("duplicate"), personalEventOp("duplicate"), personalEventOp("never-created")}
	v, err := h.submitAgentChanges(context.Background(), u, ops)
	if err != nil {
		t.Fatal(err)
	}
	result, err := h.executeAgentChanges(context.Background(), requestID(t, v), u.ID, true)
	if err != nil || result["status"] != "partial" || countAgentEvents(t, h) != 1 {
		t.Fatalf("partial: %#v %v", result, err)
	}
}
func TestAgentProfiles_Ceilings(t *testing.T) {
	member := access.Member(false)
	if mcpProfileActions("scheduling")["event_types.update"] || !mcpProfileActions("scheduling")["team_event_types.custom_times"] || !mcpProfileActions("scheduling")["dynamic_links.create"] {
		t.Fatal("scheduling profile boundary")
	}
	if !mcpProfileActions("workspace_operator")["logs.read"] || mcpProfileActions("workspace_operator")["transcripts.read"] {
		t.Fatal("operator profile boundary")
	}
	r := httptest.NewRequest("POST", "/", strings.NewReader("permissions_present=1&permissions=settings.update"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_ = r.ParseForm()
	if _, err := selectedMCPGrants(member, authRequest{Scope: "mcp"}, r); err == nil {
		t.Fatal("member granted admin authority")
	}
}
func TestAgentTeamMembers_LinksVsConfiguration(t *testing.T) {
	h, admin, _ := agentFixture(t)
	ctx := context.Background()
	for _, id := range []string{"head", "member", "outsider"} {
		if _, err := h.db.Exec(`INSERT INTO users(id,name,email,iana_timezone)VALUES(?,?,?,'UTC')`, id, id, id+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.db.Exec(`INSERT INTO teams(id,name,slug)VALUES('team-a','Team A','team-a')`); err != nil {
		t.Fatal(err)
	}
	for _, x := range []struct{ id, role string }{{"head", "owner"}, {"member", "member"}} {
		if _, err := h.db.Exec(`INSERT INTO team_members(team_id,user_id,role)VALUES('team-a',?,?)`, x.id, x.role); err != nil {
			t.Fatal(err)
		}
	}
	op := personalEventOp("team-event")
	op.Values["team_id"] = "team-a"
	_, _, err := h.callAgentOperation(ctx, admin, op)
	if err != nil {
		t.Fatal(err)
	}
	// Nobody needs to be an assigned event host just to operate a team booking link.
	for _, id := range []string{"head", "member", "outsider"} {
		grants, err := access.Load(ctx, h.db, id)
		if err != nil {
			t.Fatal(err)
		}
		user := AuthUser{ID: id, Permissions: grants, CredentialID: "test-credential"}
		link := agentOperation{Name: "create_one_time_link", Target: "team-event"}
		v, code, e := h.callAgentOperation(ctx, user, link)
		if id == "outsider" {
			if e == nil {
				t.Fatal("outsider minted team link")
			}
			continue
		}
		if e != nil || code != 201 {
			t.Fatalf("%s link: %v %d %v", id, v, code, e)
		}
		slot := time.Now().UTC().Add(48 * time.Hour)
		custom := agentOperation{Name: "offer_custom_times", Target: "team-event", Values: map[string]any{"custom_slots": []map[string]string{{"start_at": slot.Format(time.RFC3339), "end_at": slot.Add(30 * time.Minute).Format(time.RFC3339)}}}}
		if _, _, e = h.callAgentOperation(ctx, user, custom); e != nil {
			t.Fatalf("%s custom slots: %v", id, e)
		}
		_, _, e = h.callAgentOperation(ctx, user, agentOperation{Name: "update_event_type", Target: "team-event", Values: map[string]any{"name": "Edited"}})
		if id == "head" && e != nil {
			t.Fatal(e)
		}
		if id == "member" && e == nil {
			t.Fatal("member edited reusable team configuration")
		}
		// Separately granting links must not grant custom slots, or vice versa.
		delete(user.Permissions, "team_event_types.custom_times")
		if _, _, e = h.callAgentOperation(ctx, user, custom); e == nil {
			t.Fatal("custom slots accepted without their separate grant")
		}
	}

	// A compatibility admin flag for Team A must not permit moving its event to Team B.
	if _, err := h.db.Exec(`INSERT INTO teams(id,name,slug)VALUES('team-b','Team B','team-b')`); err != nil {
		t.Fatal(err)
	}
	headGrants, _ := access.Load(ctx, h.db, "head")
	head := AuthUser{ID: "head", Permissions: headGrants, CredentialID: "test-credential"}
	if _, _, err := h.callAgentOperation(ctx, head, agentOperation{Name: "update_event_type", Target: "team-event", Values: map[string]any{"team_id": "team-b"}}); err == nil {
		t.Fatal("team head moved an event to an unrelated team")
	}

	// An administrator's narrowly scoped connection cannot use their global bypass.
	admin.Permissions["team_event_types.create"] = "team"
	scoped := personalEventOp("out-of-scope-team")
	scoped.Values["team_id"] = "team-b"
	if _, _, err := h.callAgentOperation(ctx, admin, scoped); err == nil {
		t.Fatal("team-scoped admin credential created an event outside its teams")
	}

	// A former head who created the event must lose management access.
	_, _ = h.db.Exec(`UPDATE event_types SET user_id='head' WHERE slug='team-event'`)
	_, _ = h.db.Exec(`UPDATE team_members SET role='member' WHERE user_id='head'`)
	grants, _ := access.Load(ctx, h.db, "head")
	if access.EventActionAllowed(ctx, h.db, "head", grants, "update", "team-event") {
		t.Fatal("former head retained creator bypass")
	}
}

func TestAgentRequestFieldInventory(t *testing.T) {
	files, err := parser.ParseDir(token.NewFileSet(), ".", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string][]string{}
	for _, file := range files["handler"].Files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Body == nil {
				continue
			}
			if _, needed := agentRequestFields[fn.Name.Name]; !needed {
				continue
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				spec, ok := node.(*ast.ValueSpec)
				if !ok || len(spec.Names) != 1 || (spec.Names[0].Name != "req" && spec.Names[0].Name != "in" && spec.Names[0].Name != "destReq") {
					return true
				}
				st, ok := spec.Type.(*ast.StructType)
				if !ok {
					return true
				}
				keys := []string{}
				for _, field := range st.Fields.List {
					if field.Tag == nil {
						continue
					}
					tag, e := strconv.Unquote(field.Tag.Value)
					if e != nil {
						t.Fatal(e)
					}
					key := strings.Split(reflect.StructTag(tag).Get("json"), ",")[0]
					if key != "" && key != "-" {
						keys = append(keys, key)
					}
				}
				found[fn.Name.Name] = keys
				return false
			})
		}
	}
	for name, fields := range agentRequestFields {
		if !slices.Equal(fields, found[name]) {
			t.Errorf("%s request fields changed: update the agent inventory to match the REST handler", name)
		}
	}
}
func TestAgentPreferences_InteractiveOnlyAndCompanyCeiling(t *testing.T) {
	h, u, key := agentFixture(t)
	r := httptest.NewRequest("PUT", "/v1/users/me/mcp-preferences", strings.NewReader(`{"review_required":false}`))
	r.Header.Set("X-API-Key", key)
	w := httptest.NewRecorder()
	h.RequireAuth(h.PutMCPPreferences)(w, r)
	if w.Code != 403 {
		t.Fatalf("agent disabled its own gate: %d", w.Code)
	}
	cookie := securitySession(t, h, u.ID, false, false)
	w = securityCall(h, h.PutMCPPreferences, cookie, "PUT", "/v1/users/me/mcp-preferences", `{"review_required":false}`, nil)
	if w.Code != 200 {
		t.Fatalf("personal choice: %d %s", w.Code, w.Body.String())
	}
	_, err := h.db.Exec(`UPDATE server_settings SET signin_policy_json='{"mcp_review_enforced":true}' WHERE id=1`)
	if err != nil {
		t.Fatal(err)
	}
	w = securityCall(h, h.PutMCPPreferences, cookie, "PUT", "/v1/users/me/mcp-preferences", `{"review_required":false}`, nil)
	if w.Code != 403 {
		t.Fatal("overrode company policy")
	}
}
func TestAgentOperationContracts(t *testing.T) {
	h, u, _ := agentFixture(t)
	if h.agentToolVisible(access.Grants{"mcp.connect": "own", "bookings.read": "own"}, "update_event_type") {
		t.Fatal("listed ungranted configuration tool")
	}
	if !h.agentToolVisible(access.Grants{"mcp.connect": "own", "team_event_types.links": "team"}, "create_one_time_link") {
		t.Fatal("team member cannot discover link tool")
	}
	op := personalEventOp("typo")
	op.Values["duraton_minutes"] = 45
	if _, err := h.submitAgentChanges(context.Background(), u, []agentOperation{op}); err == nil {
		t.Fatal("silently discarded import field")
	}
	if _, err := h.submitAgentChanges(context.Background(), u, []agentOperation{{Name: "run_sql", Values: map[string]any{"sql": "SELECT 1"}}}); err == nil {
		t.Fatal("arbitrary operation allowed")
	}
}
func TestAgentIntegrationDraft_DisabledAndHumanCompletion(t *testing.T) {
	h, u, _ := agentFixture(t)
	_, err := h.db.Exec(`UPDATE users SET mcp_review_required=0 WHERE id=?`, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	v, err := h.submitAgentChanges(context.Background(), u, []agentOperation{{Name: "draft_identity_provider", Values: map[string]any{"kind": "microsoft", "name": "Company SSO", "purpose": "sso", "client_id": "app-id"}}})
	if err != nil || v["status"] != "completed" {
		t.Fatalf("draft: %#v %v", v, err)
	}
	var raw string
	if err = h.db.QueryRow(`SELECT configuration_json FROM mcp_integration_drafts WHERE user_id=?`, u.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var draft map[string]any
	_ = json.Unmarshal([]byte(raw), &draft)
	if draft["enabled"] != false || draft["client_secret"] != nil {
		t.Fatal("draft activated or exposed credentials")
	}
	var live int
	_ = h.db.QueryRow(`SELECT COUNT(*) FROM identity_providers WHERE client_id='app-id'`).Scan(&live)
	if live != 0 {
		t.Fatal("draft changed live sign-in policy")
	}
}

func TestAgentImport_AdvancedSettingsStayPrivate(t *testing.T) {
	h, u, _ := agentFixture(t)
	op := personalEventOp("full-import")
	op.Name = "import_event_type"
	op.Values["is_active"] = true
	op.Values["is_public"] = true
	op.Values["allow_multiple_durations"] = true
	op.Values["duration_options"] = []int{15, 30}
	op.Values["requires_confirmation"] = true
	op.Values["reminders"] = []int{24, 1}
	op.Values["buffer_before_minutes"] = 10
	result, code, err := h.callAgentOperation(context.Background(), u, op)
	if err != nil || code != 201 {
		t.Fatalf("import: %v %d %v", result, code, err)
	}
	if result.(map[string]any)["can_generate_links"] != true || result.(map[string]any)["can_offer_custom_times"] != true {
		t.Fatal("import response lost user capabilities")
	}
	var active, public, multiple, confirmation bool
	var buffer, reminders int
	if err = h.db.QueryRow(`SELECT is_active,is_public,allow_multiple_durations,requires_confirmation,buffer_before_minutes FROM event_types WHERE slug='full-import'`).Scan(&active, &public, &multiple, &confirmation, &buffer); err != nil {
		t.Fatal(err)
	}
	if active || public || !multiple || !confirmation || buffer != 10 {
		t.Fatal("import settings were discarded or published")
	}
	_ = h.db.QueryRow(`SELECT COUNT(*) FROM event_type_reminders WHERE event_type_id=(SELECT id FROM event_types WHERE slug='full-import')`).Scan(&reminders)
	if reminders != 2 {
		t.Fatal("reminders were not imported")
	}
	delete(u.Permissions, "event_types.update")
	if _, _, err = h.callAgentOperation(context.Background(), u, op); err == nil {
		t.Fatal("import bypassed update permission")
	}
}
