package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/uid"
)

func TestRBAC_SystemRolesAndMemberCannotImpersonate(t *testing.T) {
	h, database, apiKey, _ := setupWorkspaceWithDB(t)

	// Built-in admin/member roles exist and cannot be deleted or edited.
	delReq := authReq(http.MethodDelete, "/v1/roles/role_admin", "", apiKey)
	delReq.SetPathValue("id", "role_admin")
	delRec := httptest.NewRecorder()
	h.RequireAuth(h.DeleteRole)(delRec, delReq)
	if delRec.Code != http.StatusForbidden {
		t.Fatalf("delete built-in role: got %d, want 403 — %s", delRec.Code, delRec.Body.String())
	}
	updSysReq := authReq(http.MethodPatch, "/v1/roles/role_admin", `{"name":"Hacked"}`, apiKey)
	updSysReq.SetPathValue("id", "role_admin")
	updSysRec := httptest.NewRecorder()
	h.RequireAuth(h.UpdateRole)(updSysRec, updSysReq)
	if updSysRec.Code != http.StatusForbidden {
		t.Fatalf("edit built-in role: got %d, want 403 — %s", updSysRec.Code, updSysRec.Body.String())
	}

	// Seed two members; member1 tries to impersonate member2 -> 403.
	m1ID := uid.New()
	m2ID := uid.New()
	mustExec(t, database, `INSERT INTO users (id, email, name, iana_timezone) VALUES (?, 'm1@example.com', 'Member 1', 'UTC')`, m1ID)
	mustExec(t, database, `INSERT INTO users (id, email, name, iana_timezone) VALUES (?, 'm2@example.com', 'Member 2', 'UTC')`, m2ID)
	sessID := uid.New()
	exp := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	mustExec(t, database, `INSERT INTO sessions (id, user_id, expires_at) VALUES (?, ?, ?)`, sessID, m1ID, exp)

	impReq := httptest.NewRequest(http.MethodPost, "/v1/admin/impersonate", strings.NewReader(`{"user_id":"`+m2ID+`"}`))
	impReq.Header.Set("Content-Type", "application/json")
	impReq.AddCookie(&http.Cookie{Name: "calnode_session", Value: sessID})
	impRec := httptest.NewRecorder()
	h.RequireAuth(h.StartImpersonation)(impRec, impReq)
	if impRec.Code != http.StatusForbidden {
		t.Fatalf("member should not be allowed to impersonate: got %d — %s", impRec.Code, impRec.Body.String())
	}
}

func TestRBAC_AdminImpersonatesMember_And_ProtectedCustomRoleBlocked(t *testing.T) {
	h, database, apiKey, ownerID := setupWorkspaceWithDB(t)

	memberID := uid.New()
	vipID := uid.New()
	mustExec(t, database, `INSERT INTO users (id, email, name, iana_timezone) VALUES (?, 'member@example.com', 'Member One', 'UTC')`, memberID)
	mustExec(t, database, `INSERT INTO users (id, email, name, iana_timezone) VALUES (?, 'vip@example.com', 'VIP Exec', 'UTC')`, vipID)

	// 1. Create a protected custom role (is_impersonatable = false) and assign to VIP.
	cReq := authReq(http.MethodPost, "/v1/roles", `{"name":"Executive","can_impersonate":false,"is_impersonatable":false}`, apiKey)
	cRec := httptest.NewRecorder()
	h.RequireAuth(h.CreateRole)(cRec, cReq)
	if cRec.Code != http.StatusCreated {
		t.Fatalf("CreateRole: got %d — %s", cRec.Code, cRec.Body.String())
	}
	var roleResp struct {
		ID               string `json:"id"`
		IsImpersonatable bool   `json:"is_impersonatable"`
	}
	_ = json.Unmarshal(cRec.Body.Bytes(), &roleResp)
	if roleResp.IsImpersonatable {
		t.Fatalf("expected is_impersonatable=false")
	}

	aReq := authReq(http.MethodPatch, "/v1/users/"+vipID+"/custom-role", `{"custom_role_id":"`+roleResp.ID+`"}`, apiKey)
	aReq.SetPathValue("id", vipID)
	aRec := httptest.NewRecorder()
	h.RequireAuth(h.SetUserCustomRole)(aRec, aReq)
	if aRec.Code != http.StatusOK {
		t.Fatalf("SetUserCustomRole: got %d — %s", aRec.Code, aRec.Body.String())
	}

	// Custom role CAN be edited.
	uReq := authReq(http.MethodPatch, "/v1/roles/"+roleResp.ID, `{"name":"Executive VIP","can_impersonate":false,"is_impersonatable":false,"can_manage_teams":true}`, apiKey)
	uReq.SetPathValue("id", roleResp.ID)
	uRec := httptest.NewRecorder()
	h.RequireAuth(h.UpdateRole)(uRec, uReq)
	if uRec.Code != http.StatusOK {
		t.Fatalf("UpdateRole: got %d — %s", uRec.Code, uRec.Body.String())
	}

	// 2. Owner/admin session cannot impersonate VIP (is_impersonatable = false) -> 403.
	sessID := uid.New()
	exp := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	mustExec(t, database, `INSERT INTO sessions (id, user_id, expires_at) VALUES (?, ?, ?)`, sessID, ownerID, exp)

	vipImpReq := httptest.NewRequest(http.MethodPost, "/v1/admin/impersonate", strings.NewReader(`{"user_id":"`+vipID+`"}`))
	vipImpReq.Header.Set("Content-Type", "application/json")
	vipImpReq.AddCookie(&http.Cookie{Name: "calnode_session", Value: sessID})
	vipImpRec := httptest.NewRecorder()
	h.RequireAuth(h.StartImpersonation)(vipImpRec, vipImpReq)
	if vipImpRec.Code != http.StatusForbidden {
		t.Fatalf("protected role should block impersonation with 403, got %d — %s", vipImpRec.Code, vipImpRec.Body.String())
	}

	// 3. Owner/admin CAN impersonate built-in Member One -> 200.
	startReq := httptest.NewRequest(http.MethodPost, "/v1/admin/impersonate", strings.NewReader(`{"user_id":"`+memberID+`"}`))
	startReq.Header.Set("Content-Type", "application/json")
	startReq.AddCookie(&http.Cookie{Name: "calnode_session", Value: sessID})
	startRec := httptest.NewRecorder()
	h.RequireAuth(h.StartImpersonation)(startRec, startReq)
	if startRec.Code != http.StatusOK {
		t.Fatalf("StartImpersonation: got %d — %s", startRec.Code, startRec.Body.String())
	}

	// 4. GET /v1/users/me returns Member One with impersonating=true.
	meReq := httptest.NewRequest(http.MethodGet, "/v1/users/me", nil)
	meReq.AddCookie(&http.Cookie{Name: "calnode_session", Value: sessID})
	meRec := httptest.NewRecorder()
	h.RequireAuth(h.GetMe)(meRec, meReq)
	if meRec.Code != http.StatusOK {
		t.Fatalf("GetMe while impersonating: got %d — %s", meRec.Code, meRec.Body.String())
	}
	var meResp struct {
		ID                string `json:"id"`
		Impersonating     bool   `json:"impersonating"`
		ImpersonatorEmail string `json:"impersonator_email"`
	}
	_ = json.Unmarshal(meRec.Body.Bytes(), &meResp)
	if meResp.ID != memberID || !meResp.Impersonating || meResp.ImpersonatorEmail != "host@example.com" {
		t.Fatalf("unexpected GetMe while impersonating: %+v", meResp)
	}

	// 5. Stop impersonation + check audit log.
	stopReq := httptest.NewRequest(http.MethodPost, "/v1/admin/impersonate/stop", nil)
	stopReq.AddCookie(&http.Cookie{Name: "calnode_session", Value: sessID})
	stopRec := httptest.NewRecorder()
	h.RequireAuth(h.StopImpersonation)(stopRec, stopReq)
	if stopRec.Code != http.StatusOK {
		t.Fatalf("StopImpersonation: got %d — %s", stopRec.Code, stopRec.Body.String())
	}

	logReq := authReq(http.MethodGet, "/v1/admin/impersonate/logs", "", apiKey)
	logRec := httptest.NewRecorder()
	h.RequireAuth(h.ListImpersonationLogs)(logRec, logReq)
	if logRec.Code != http.StatusOK {
		t.Fatalf("ListImpersonationLogs: got %d — %s", logRec.Code, logRec.Body.String())
	}
	var logResp struct {
		Items []struct {
			Action string `json:"action"`
		} `json:"items"`
	}
	_ = json.Unmarshal(logRec.Body.Bytes(), &logResp)
	if len(logResp.Items) != 2 {
		t.Fatalf("expected 2 audit log entries (start+stop), got %d", len(logResp.Items))
	}
}

func TestRoleDescriptionCreateUpdateAndList(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)
	create := authReq(http.MethodPost, "/v1/roles", `{"name":"Support","description":"Handles customer requests","can_manage_users":true}`, key)
	out := httptest.NewRecorder()
	h.RequireAuth(h.CreateRole)(out, create)
	if out.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", out.Code, out.Body)
	}
	var role struct {
		ID          string `json:"id"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(out.Body.Bytes(), &role); err != nil || role.Description != "Handles customer requests" {
		t.Fatalf("create response: %s (%v)", out.Body, err)
	}
	// Existing clients that omit description on update must not erase it.
	update := authReq(http.MethodPatch, "/v1/roles/"+role.ID, `{"name":"Support team","can_manage_users":true}`, key)
	update.SetPathValue("id", role.ID)
	changed := httptest.NewRecorder()
	h.RequireAuth(h.UpdateRole)(changed, update)
	if changed.Code != http.StatusOK {
		t.Fatalf("update: %d %s", changed.Code, changed.Body)
	}
	if err := json.Unmarshal(changed.Body.Bytes(), &role); err != nil || role.Description != "Handles customer requests" {
		t.Fatalf("update response: %s (%v)", changed.Body, err)
	}
	edit := authReq(http.MethodPatch, "/v1/roles/"+role.ID, `{"name":"Support team","description":"Handles scheduling","can_manage_users":true}`, key)
	edit.SetPathValue("id", role.ID)
	edited := httptest.NewRecorder()
	h.RequireAuth(h.UpdateRole)(edited, edit)
	if edited.Code != http.StatusOK {
		t.Fatalf("edit: %d %s", edited.Code, edited.Body)
	}
	if err := json.Unmarshal(edited.Body.Bytes(), &role); err != nil || role.Description != "Handles scheduling" {
		t.Fatalf("edit response: %s (%v)", edited.Body, err)
	}
	list := httptest.NewRecorder()
	h.RequireAuth(h.ListRoles)(list, authReq(http.MethodGet, "/v1/roles", "", key))
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "Handles scheduling") {
		t.Fatalf("list: %d %s", list.Code, list.Body)
	}
}
