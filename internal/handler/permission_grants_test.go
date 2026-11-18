package handler_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/access"
	"github.com/calnode/calnode/internal/uid"
)

func sessionRequest(t *testing.T, db *sql.DB, userID, method, path, body string) *http.Request {
	t.Helper()
	id := uid.New()
	now := time.Now().UTC()
	mustExec(t, db, `INSERT INTO sessions(id,user_id,expires_at,reauthenticated_at) VALUES(?,?,?,?)`, id, userID, now.Add(time.Hour).Format(time.RFC3339), now.Format(time.RFC3339Nano))
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: "calnode_session", Value: id})
	return r
}

func TestPermissionGrants_keysAreExplicitAndCannotAmplify(t *testing.T) {
	h, db, ownerKey, owner := setupWorkspaceWithDB(t)
	// A key cannot create more credentials, even when its human owner is an Admin.
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateAPIKey)(rec, authReq("POST", "/v1/api-keys", `{"name":"nested","permissions":{"bookings.read":"all"}}`, ownerKey))
	mustStatus(t, rec, 403, "nested key")
	for _, body := range []string{`{"name":"missing"}`, `{"name":"bad","permissions":{"unknown.read":"all"}}`, `{"name":"bad","permissions":{"roles.manage":"all"}}`} {
		rec = httptest.NewRecorder()
		h.RequireAuth(h.CreateAPIKey)(rec, sessionRequest(t, db, owner, "POST", "/v1/api-keys", body))
		if rec.Code != 400 && rec.Code != 403 {
			t.Fatalf("invalid grant accepted: %d %s", rec.Code, rec.Body.String())
		}
	}
	rec = httptest.NewRecorder()
	h.RequireAuth(h.CreateAPIKey)(rec, sessionRequest(t, db, owner, "POST", "/v1/api-keys", `{"name":"read-only","permissions":{"event_types.read":"own"}}`))
	mustStatus(t, rec, 201, "create scoped key")
	var key struct{ ID, Key string }
	if json.Unmarshal(rec.Body.Bytes(), &key) != nil || key.Key == "" {
		t.Fatal(rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.RequireAuth(h.ListEventTypes)(rec, authReq("GET", "/v1/event-types", "", key.Key))
	mustStatus(t, rec, 200, "allowed read")
	rec = httptest.NewRecorder()
	h.RequireAuth(h.CreateInvite)(rec, authReq("POST", "/v1/invites", `{"email":"x@example.com"}`, key.Key))
	mustStatus(t, rec, 403, "denied mutation")
	rec = httptest.NewRecorder()
	h.RequireAuth(h.ListAPIKeys)(rec, sessionRequest(t, db, owner, "GET", "/v1/api-keys", ""))
	mustStatus(t, rec, 200, "inventory")
	if strings.Contains(rec.Body.String(), key.Key) {
		t.Fatal("inventory exposed secret")
	}
	mustExec(t, db, `UPDATE api_keys SET expires_at='2000-01-01T00:00:00Z' WHERE id=?`, key.ID)
	rec = httptest.NewRecorder()
	h.RequireAuth(h.ListEventTypes)(rec, authReq("GET", "/v1/event-types", "", key.Key))
	mustStatus(t, rec, 401, "expired key")
}

func TestPermissionGrants_promotionDoesNotUpgradeKeyAndRevocationIsLive(t *testing.T) {
	h, db, ownerKey, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, ownerKey)
	member := "scoped-member"
	mustExec(t, db, `INSERT INTO users(id,email,name) VALUES(?,'scoped@example.com','Scoped')`, member)
	mustExec(t, db, `INSERT INTO roles(id,name,permissions_json) VALUES('reader','reader','{"event_types.read":"own","api_keys.create":"own"}')`)
	mustExec(t, db, `UPDATE users SET custom_role_id='reader' WHERE id=?`, member)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateAPIKey)(rec, sessionRequest(t, db, member, "POST", "/v1/api-keys", `{"name":"reader","permissions":{"event_types.read":"all"}}`))
	mustStatus(t, rec, 403, "overgrant")
	rec = httptest.NewRecorder()
	h.RequireAuth(h.CreateAPIKey)(rec, sessionRequest(t, db, member, "POST", "/v1/api-keys", `{"name":"reader","permissions":{"event_types.read":"own"}}`))
	mustStatus(t, rec, 201, "member scoped key")
	var key struct{ Key string }
	_ = json.Unmarshal(rec.Body.Bytes(), &key)
	mustExec(t, db, `UPDATE users SET is_admin=1 WHERE id=?`, member)
	req := authReq("GET", "/v1/event-types/"+slug, "", key.Key)
	req.SetPathValue("slug", slug)
	rec = httptest.NewRecorder()
	h.RequireAuth(h.GetEventType)(rec, req)
	mustStatus(t, rec, 404, "promotion must not widen key resources")
	mustExec(t, db, `UPDATE users SET is_admin=0 WHERE id=?`, member)
	mustExec(t, db, `UPDATE roles SET permissions_json='{}' WHERE id='reader'`)
	rec = httptest.NewRecorder()
	h.RequireAuth(h.ListEventTypes)(rec, authReq("GET", "/v1/event-types", "", key.Key))
	mustStatus(t, rec, 403, "live role revocation")
}

func TestPermissionGrants_adminInvitationIsBoundAndIssuerRevocationApplies(t *testing.T) {
	h, db, _, owner := setupWorkspaceWithDB(t)
	create := func(email, role string) string {
		t.Helper()
		rec := httptest.NewRecorder()
		h.RequireAuth(h.CreateInvite)(rec, sessionRequest(t, db, owner, "POST", "/v1/invites", `{"email":"`+email+`","role":"`+role+`"}`))
		mustStatus(t, rec, 201, "invite")
		var out struct {
			URL string `json:"invite_url"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		parts := strings.Split(out.URL, "/")
		return parts[len(parts)-1]
	}
	token := create("new-admin@example.com", "admin")
	claim := func(tok string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/invites/"+tok+"/claim", strings.NewReader(`{"name":"New Admin","password":"a-strong-test-password","role":"owner"}`))
		r.SetPathValue("token", tok)
		rec := httptest.NewRecorder()
		h.ClaimInvite(rec, r)
		return rec
	}
	mustStatus(t, claim(token), 201, "admin claim")
	var admin, owns bool
	if db.QueryRow(`SELECT is_admin,is_owner FROM users WHERE email='new-admin@example.com'`).Scan(&admin, &owns) != nil || !admin || owns {
		t.Fatal("claim changed invitation authority")
	}
	mustStatus(t, claim(token), 404, "single-use invite")
	token = create("revoked@example.com", "admin")
	mustExec(t, db, `UPDATE users SET is_owner=0,is_admin=0,legacy_permissions=0 WHERE id=?`, owner)
	mustStatus(t, claim(token), 403, "demoted issuer cannot grant admin")
}

func TestPermissionGrants_ownershipClaimIsAtomicAndNotAvailableToKeys(t *testing.T) {
	h, db, key, owner := setupWorkspaceWithDB(t)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ClaimOwnership)(rec, authReq("POST", "/v1/users/me/claim-ownership", "{}", key))
	mustStatus(t, rec, 403, "key cannot claim ownership")
	mustExec(t, db, `UPDATE users SET is_owner=0 WHERE id=?`, owner)
	mustExec(t, db, `UPDATE server_settings SET ownership_recovery_enabled=1`)
	second := "second-admin"
	mustExec(t, db, `INSERT INTO users(id,email,name,is_admin) VALUES(?,'second@example.com','Second',1)`, second)
	requests := []*http.Request{sessionRequest(t, db, owner, "POST", "/claim", "{}"), sessionRequest(t, db, second, "POST", "/claim", "{}")}
	codes := make([]int, 2)
	var wg sync.WaitGroup
	for i, req := range requests {
		wg.Add(1)
		go func(i int, r *http.Request) {
			defer wg.Done()
			rec := httptest.NewRecorder()
			h.RequireAuth(h.ClaimOwnership)(rec, r)
			codes[i] = rec.Code
		}(i, req)
	}
	wg.Wait()
	successes := 0
	for _, code := range codes {
		if code == 200 {
			successes++
		} else if code != 409 {
			t.Fatalf("unexpected claim result: %v", codes)
		}
	}
	var count int
	_ = db.QueryRow(`SELECT COUNT(*) FROM users WHERE is_owner=1`).Scan(&count)
	if count != 1 || successes != 1 {
		t.Fatalf("owners=%d results=%v", count, codes)
	}
	var recovery bool
	_ = db.QueryRow(`SELECT ownership_recovery_enabled FROM server_settings`).Scan(&recovery)
	if recovery {
		t.Fatal("recovery remained enabled")
	}
}

// Verify the same vocabulary rejects unknown actions and cannot widen resource scope.
func TestPermissionGrants_catalogValidation(t *testing.T) {
	if access.Validate(access.Grants{"bookings.read": "all"}, access.Grants{"bookings.read": "own"}, true) == nil {
		t.Fatal("scope amplification accepted")
	}
	if access.Validate(access.Grants{"api_keys.create": "own"}, access.Full(), true) == nil {
		t.Fatal("credential minting delegated")
	}
}

func TestPermissionGrants_adminInvitationsNeedExplicitDelegation(t *testing.T) {
	h, database, _, owner := setupWorkspaceWithDB(t)
	for _, allowAdmin := range []bool{false, true} {
		grants := access.Grants{"members.invite_member": "all"}
		if allowAdmin {
			grants["members.invite_admin"] = "all"
		}
		body, _ := json.Marshal(map[string]any{"name": "invite automation", "permissions": grants})
		created := httptest.NewRecorder()
		h.RequireAuth(h.CreateAPIKey)(created, sessionRequest(t, database, owner, "POST", "/v1/api-keys", string(body)))
		mustStatus(t, created, 201, "create invitation credential")
		var key struct{ Key string }
		if err := json.Unmarshal(created.Body.Bytes(), &key); err != nil {
			t.Fatal(err)
		}
		invited := httptest.NewRecorder()
		h.RequireAuth(h.CreateInvite)(invited, authReq("POST", "/v1/invites", `{"email":"explicit-admin@example.com","role":"admin"}`, key.Key))
		want := 403
		if allowAdmin {
			want = 201
		}
		mustStatus(t, invited, want, "explicit admin invitation permission")
	}
}

func TestPermissionGrants_adminAndCustomRoleAreMutuallyExclusive(t *testing.T) {
	h, database, _, owner := setupWorkspaceWithDB(t)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateInvite)(rec, sessionRequest(t, database, owner, "POST", "/v1/invites", `{"email":"ambiguous@example.com","role":"admin","custom_role_id":"restricted"}`))
	mustStatus(t, rec, 400, "ambiguous invitation authority")
}

func TestPermissionGrants_legacyManagementCannotGainAdminDelegation(t *testing.T) {
	grants := access.LegacyRole(true, false, false, false)
	if grants.Has("members.invite_admin") || grants.Has("members.update_role") {
		t.Fatalf("legacy role gained admin delegation: %v", grants)
	}
	if !grants.Has("members.read") || !grants.Has("members.invite_member") {
		t.Fatal("legacy member management was lost")
	}
	h, database, _, _ := setupWorkspaceWithDB(t)
	mustExec(t, database, `INSERT INTO roles(id,name,can_manage_users) VALUES('legacy-manager','Legacy manager',1)`)
	mustExec(t, database, `INSERT INTO users(id,email,name,custom_role_id) VALUES('legacy-manager','legacy@example.com','Manager','legacy-manager')`)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateInvite)(rec, sessionRequest(t, database, "legacy-manager", "POST", "/v1/invites", `{"email":"admin-injection@example.com","role":"admin"}`))
	mustStatus(t, rec, 403, "legacy admin invitation")
}
