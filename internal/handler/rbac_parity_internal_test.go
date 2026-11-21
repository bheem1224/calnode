package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/access"
	"github.com/calnode/calnode/internal/booking"
)

func rbacExec(t *testing.T, h *Handler, q string, args ...any) {
	t.Helper()
	if _, err := h.db.Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}

func rbacFixture(t *testing.T) *Handler {
	t.Helper()
	h, owner, _ := agentFixture(t)
	for _, team := range []string{"a", "b"} {
		rbacExec(t, h, `INSERT INTO teams(id,name,slug)VALUES(?,?,?)`, team, team, team)
	}
	for _, e := range []struct{ id, team string }{{"personal", ""}, {"team-a", "a"}, {"team-b", "b"}} {
		rbacExec(t, h, `INSERT INTO event_types(id,slug,name,user_id,team_id,duration_minutes,is_active,is_public,location_type,location_value)VALUES(?,?,?,?,NULLIF(?,''),30,1,1,'phone','+15551234567')`, e.id, e.id, e.id, owner.ID, e.team)
	}
	return h
}

// Seed actual persisted roles, sessions and frozen credentials. Every assertion
// below passes through authentication and live role/credential intersection.
func rbacActor(t *testing.T, h *Handler, id string, g access.Grants, team, role string) {
	t.Helper()
	g["mcp.connect"] = "own"
	rbacExec(t, h, `INSERT INTO roles(id,name,permissions_json)VALUES(?,?,?)`, id, id, g.JSON())
	rbacExec(t, h, `INSERT INTO users(id,name,email,custom_role_id,iana_timezone)VALUES(?,?,?,?,'UTC')`, id, id, id+"@example.test", id)
	if team != "" {
		rbacExec(t, h, `INSERT INTO team_members(team_id,user_id,role)VALUES(?,?,?)`, team, id, role)
	}
	rbacExec(t, h, `INSERT INTO sessions(id,user_id,expires_at)VALUES(?,?,'2099-01-01T00:00:00Z')`, id+"-session", id)
	rbacExec(t, h, `INSERT INTO api_keys(id,user_id,name,key_hash,permissions_json,expires_at)VALUES(?,?,'Matrix',?,?,'2099-01-01T00:00:00Z')`, id+"-key", id, hashAPIKey(id+"-key-secret"), g.JSON())
	rbacExec(t, h, `INSERT INTO oauth_access_tokens(id,user_id,client_id,token_hash,permissions_json,resource,expires_at,created_at)VALUES(?,?,'matrix-client',?,?,?,'2099-01-01T00:00:00Z','2026-11-21T00:00:00Z')`, id+"-oauth", id, hashAPIKey(id+"-oauth-secret"), g.JSON(), h.baseURL+"/mcp")
}

func rbacCall(t *testing.T, h *Handler, channel, actor, operation, target, body string, all bool) (int, string) {
	t.Helper()
	if channel == "mcp" {
		u, _, err := h.mcpPrincipal(context.Background(), actor+"-oauth-secret")
		if err != nil {
			t.Fatal(err)
		}
		var values map[string]any
		if body != "" {
			if err = json.Unmarshal([]byte(body), &values); err != nil {
				t.Fatal(err)
			}
		}
		op := agentOperation{Name: operation, Target: target, Values: values}
		if all {
			op.Query = map[string]string{"scope": "all"}
		}
		v, code, _ := h.callAgentOperation(context.Background(), u, op)
		raw, _ := json.Marshal(v)
		return code, string(raw)
	}
	method, path, fn := "GET", "/v1/event-types", http.HandlerFunc(h.ListEventTypes)
	switch operation {
	case "create_event_type":
		method = "POST"
		fn = h.CreateEventType
	case "get_event_type_configuration":
		path += "/" + target
		fn = h.GetEventType
	case "update_event_type":
		method = "PATCH"
		path += "/" + target
		fn = h.PatchEventType
	case "create_one_time_link", "offer_custom_times":
		method = "POST"
		path += "/" + target + "/single-use-links"
		fn = h.CreateSingleUseLink
	case "get_audit_logs":
		path = "/v1/security/audit"
		fn = h.ListSecurityAudit
	}
	if all {
		path += "?scope=all"
	}
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.SetPathValue("slug", target)
	r.SetPathValue("id", target)
	if channel == "key" {
		r.Header.Set("X-API-Key", actor+"-key-secret")
	} else {
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: actor + "-session"})
	}
	w := httptest.NewRecorder()
	h.RequireAuth(fn)(w, r)
	return w.Code, w.Body.String()
}

func TestRBACParity_EventConfigurationAndLinkBoundaries(t *testing.T) {
	h := rbacFixture(t)
	start := time.Now().UTC().Add(48 * time.Hour)
	custom, _ := json.Marshal(map[string]any{"custom_slots": []map[string]string{{"start_at": start.Format(time.RFC3339), "end_at": start.Add(30 * time.Minute).Format(time.RFC3339)}}})
	tests := []struct {
		name, permission, scope, team, role, target, op string
		allowed, assigned                               bool
	}{
		{"assigned-own", "event_types.update", "own", "", "", "personal", "update_event_type", false, true},
		{"assigned-old-team", "event_types.update", "team", "", "", "personal", "update_event_type", false, true},
		{"owner-own", "event_types.update", "own", "", "", "self", "update_event_type", true, false},
		{"owner-old-team", "event_types.update", "team", "", "", "self", "update_event_type", true, false},
		{"personal-all", "event_types.update", "all", "", "", "personal", "update_event_type", true, false},
		{"head-own-personal", "event_types.update", "own", "a", "owner", "personal", "update_event_type", false, false},
		{"member-config", "team_event_types.update", "team", "a", "member", "team-a", "update_event_type", false, false},
		{"head-config", "team_event_types.update", "team", "a", "owner", "team-a", "update_event_type", true, false},
		{"head-explicit-deny", "event_types.update", "own", "a", "owner", "team-a", "update_event_type", false, false},
		{"unrelated-team", "team_event_types.update", "team", "a", "owner", "team-b", "update_event_type", false, false},
		{"global-not-head", "team_event_types.update", "all", "a", "owner", "team-b", "update_event_type", false, false},
		{"member-links", "team_event_types.links", "team", "a", "member", "team-a", "create_one_time_link", true, false},
		{"member-custom", "team_event_types.custom_times", "team", "a", "member", "team-a", "offer_custom_times", true, false},
		{"links-not-custom", "team_event_types.links", "team", "a", "member", "team-a", "offer_custom_times", false, false},
		{"custom-not-links", "team_event_types.custom_times", "team", "a", "member", "team-a", "create_one_time_link", false, false},
		{"outsider-links", "team_event_types.links", "team", "b", "member", "team-a", "create_one_time_link", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rbacActor(t, h, tt.name, access.Grants{tt.permission: tt.scope}, tt.team, tt.role)
			if tt.target == "self" {
				tt.target = tt.name + "-owned"
				rbacExec(t, h, `INSERT INTO event_types(id,slug,name,user_id,duration_minutes,location_type,location_value)VALUES(?,?,?, ?,30,'phone','+15551234567')`, tt.target, tt.target, tt.target, tt.name)
			}
			if tt.assigned {
				rbacExec(t, h, `INSERT INTO event_type_hosts(id,event_type_id,user_id,role)VALUES(?,'personal',?,'required')`, tt.name, tt.name)
			}
			body := `{"name":"Edited"}`
			if tt.op == "create_one_time_link" {
				body = `{}`
			} else if tt.op == "offer_custom_times" {
				body = string(custom)
			}
			for _, channel := range []string{"session", "key", "mcp"} {
				code, response := rbacCall(t, h, channel, tt.name, tt.op, tt.target, body, false)
				if (code < 300) != tt.allowed || (!tt.allowed && code != 403 && code != 404) {
					t.Errorf("%s: allowed=%v got %d %s", channel, tt.allowed, code, response)
				}
			}
		})
	}
}

func TestRBACParity_CreateScopesAndLiveHeadDemotion(t *testing.T) {
	h := rbacFixture(t)
	for _, tt := range []struct {
		name, permission, team, role, destination string
		allowed                                   bool
	}{
		{"personal-creator", "event_types.create", "", "", "", true},
		{"head-creator", "team_event_types.create", "a", "owner", "a", true},
		{"member-creator", "team_event_types.create", "a", "member", "a", false},
		{"wrong-team-creator", "team_event_types.create", "a", "owner", "b", false},
	} {
		scope := "own"
		if tt.permission == "team_event_types.create" {
			scope = "team"
		}
		rbacActor(t, h, tt.name, access.Grants{tt.permission: scope}, tt.team, tt.role)
		for _, channel := range []string{"session", "key", "mcp"} {
			body, _ := json.Marshal(map[string]any{"slug": tt.name + "-" + channel, "name": "Created", "duration_minutes": 30, "team_id": tt.destination, "location_type": "phone", "location_value": "+15551234567"})
			code, response := rbacCall(t, h, channel, tt.name, "create_event_type", "", string(body), false)
			if (code == 201) != tt.allowed || (!tt.allowed && code != 403 && code != 404) {
				t.Errorf("%s/%s: %d %s", tt.name, channel, code, response)
			}
		}
	}
	rbacActor(t, h, "demoted-head", access.Grants{"team_event_types.update": "team"}, "a", "owner")
	for _, channel := range []string{"session", "key", "mcp"} {
		code, body := rbacCall(t, h, channel, "demoted-head", "update_event_type", "team-a", `{"name":"Before demotion"}`, false)
		if code != 200 {
			t.Fatal(channel, code, body)
		}
	}
	rbacExec(t, h, `UPDATE team_members SET role='member' WHERE team_id='a' AND user_id='demoted-head'`)
	for _, channel := range []string{"session", "key", "mcp"} {
		code, _ := rbacCall(t, h, channel, "demoted-head", "update_event_type", "team-a", `{"name":"After demotion"}`, false)
		if code != 403 && code != 404 {
			t.Errorf("%s retained team configuration after demotion: %d", channel, code)
		}
	}
}

func TestRBACParity_ListGetAndSchedulingMCP(t *testing.T) {
	h := rbacFixture(t)
	for _, tt := range []struct {
		name string
		g    access.Grants
		team string
		want []string
	}{
		{"mixed", access.Grants{"event_types.read": "own", "team_event_types.read": "all"}, "", []string{"team-a", "team-b"}},
		{"team-only", access.Grants{"team_event_types.read": "team"}, "a", []string{"team-a"}},
		{"personal-only", access.Grants{"event_types.read": "all"}, "", []string{"personal"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rbacActor(t, h, tt.name, tt.g, tt.team, "member")
			for _, channel := range []string{"session", "key", "mcp"} {
				code, body := rbacCall(t, h, channel, tt.name, "list_event_type_configurations", "", "", true)
				if code != 200 {
					t.Fatal(channel, code, body)
				}
				for _, id := range []string{"personal", "team-a", "team-b"} {
					want := false
					for _, v := range tt.want {
						want = want || v == id
					}
					if strings.Contains(body, `"slug":"`+id+`"`) != want {
						t.Errorf("%s list boundary %s: %s", channel, id, body)
					}
					status, _ := rbacCall(t, h, channel, tt.name, "get_event_type_configuration", id, "", false)
					if (status == 200) != want {
						t.Errorf("%s get/list disagree for %s: %d", channel, id, status)
					}
				}
			}
			u, _, err := h.mcpPrincipal(context.Background(), tt.name+"-oauth-secret")
			if err != nil {
				t.Fatal(err)
			}
			ctx := withMCPCaller(context.Background(), mcpCaller{UserID: u.ID, Permissions: u.Permissions, CredentialID: u.CredentialID})
			_, out, err := h.mcpListEventTypes(ctx, nil, listEventTypesIn{})
			if err != nil {
				t.Fatal(err)
			}
			if len(out.EventTypes) != len(tt.want) {
				t.Fatalf("scheduling/configuration lists disagree: %#v", out)
			}
		})
	}
}

func TestRBACParity_AuditLogsAndLiveRevocation(t *testing.T) {
	h := rbacFixture(t)
	for _, tt := range []struct {
		name, permission string
		status           int
	}{{"log-reader", "logs.read", 200}, {"history-reader", "impersonation.audit", 403}} {
		rbacActor(t, h, tt.name, access.Grants{tt.permission: "all"}, "", "")
		for _, channel := range []string{"session", "key", "mcp"} {
			code, body := rbacCall(t, h, channel, tt.name, "get_audit_logs", "", "", false)
			if code != tt.status {
				t.Errorf("%s %s: %d %s", tt.name, channel, code, body)
			}
		}
	}
	rbacExec(t, h, `UPDATE roles SET permissions_json='{"mcp.connect":"own"}' WHERE id='log-reader'`)
	for _, channel := range []string{"session", "key", "mcp"} {
		code, _ := rbacCall(t, h, channel, "log-reader", "get_audit_logs", "", "", false)
		if code != 403 {
			t.Errorf("%s retained revoked permission: %d", channel, code)
		}
	}
}

// A reusable event assignment is not participation in every booking made from
// it. Assigned-teams grants must agree with the booking list's actual boundary.
func TestRBACParity_BookingTeamScopeMatchesLists(t *testing.T) {
	h := rbacFixture(t)
	var owner string
	if err := h.db.QueryRow(`SELECT user_id FROM event_types WHERE id='personal'`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	for index, tt := range []struct {
		name, scope, team, event           string
		assigned, ownsEvent, host, allowed bool
	}{
		{"assigned-event-host", "team", "", "personal", true, false, false, false},
		{"event-owner-only", "team", "", "personal", false, true, false, false},
		{"actual-booking-host", "own", "", "personal", false, false, true, true},
		{"booking-team-member", "team", "a", "team-a", false, false, false, true},
		{"booking-unrelated-team", "team", "b", "team-a", false, false, false, false},
		{"booking-instance-reader", "all", "", "personal", false, false, false, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rbacActor(t, h, tt.name, access.Grants{"bookings.read": tt.scope}, tt.team, "member")
			if tt.assigned {
				rbacExec(t, h, `INSERT INTO event_type_hosts(id,event_type_id,user_id,role)VALUES(?,'personal',?,'required')`, tt.name, tt.name)
			}
			if tt.ownsEvent {
				tt.event = tt.name
				rbacExec(t, h, `INSERT INTO event_types(id,slug,name,user_id,duration_minutes)VALUES(?,?,?,?,30)`, tt.event, tt.event, tt.event, tt.name)
			}
			host := owner
			if tt.host {
				host = tt.name
			}
			start := time.Date(2090, 1, 1, 10, 0, 0, 0, time.UTC).Add(time.Duration(index) * time.Hour)
			rbacExec(t, h, `INSERT INTO bookings(id,event_type_id,host_id,start_at,end_at,status)VALUES(?,?,?,?,?,'confirmed')`, tt.name, tt.event, host, start.Format(time.RFC3339), start.Add(30*time.Minute).Format(time.RFC3339))
			f := booking.ListFilter{ViewerID: tt.name, Limit: 100}
			if tt.scope == "all" {
				f.ViewerID = ""
			} else if tt.scope == "team" {
				f.ViewerTeamID = tt.name
			}
			items, err := h.bookingSvc.List(context.Background(), f)
			if err != nil {
				t.Fatal(err)
			}
			listed := false
			for _, b := range items {
				listed = listed || b.ID == tt.name
			}
			if listed != tt.allowed {
				t.Fatal("unexpected list boundary", listed, tt.allowed)
			}
			for _, channel := range []string{"session", "key", "mcp"} {
				if channel == "mcp" {
					u, _, err := h.mcpPrincipal(context.Background(), tt.name+"-oauth-secret")
					if err != nil {
						t.Fatal(err)
					}
					ctx := withMCPCaller(context.Background(), mcpCaller{UserID: u.ID, Permissions: u.Permissions, CredentialID: u.CredentialID})
					_, _, err = h.mcpGetBooking(ctx, nil, getBookingIn{BookingID: tt.name})
					if (err == nil) != listed {
						t.Errorf("MCP get/list disagree: listed=%v error=%v", listed, err)
					}
					continue
				}
				r := httptest.NewRequest("GET", "/v1/bookings/"+tt.name, nil)
				r.SetPathValue("id", tt.name)
				if channel == "key" {
					r.Header.Set("X-API-Key", tt.name+"-key-secret")
				} else {
					r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tt.name + "-session"})
				}
				w := httptest.NewRecorder()
				h.RequireAuth(h.GetBookingPrivate)(w, r)
				if (w.Code == 200) != listed {
					t.Errorf("%s get/list disagree: listed=%v status=%d", channel, listed, w.Code)
				}
			}
		})
	}
}
