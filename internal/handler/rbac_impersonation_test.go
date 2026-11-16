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

func TestRBAC_SystemRolesProtectedAndNoDefaultImpersonation(t *testing.T) {
	h, database, apiKey, ownerID := setupWorkspaceWithDB(t)

	// Built-in admin/member roles exist and cannot be deleted.
	delReq := authReq(http.MethodDelete, "/v1/roles/role_admin", "", apiKey)
	delReq.SetPathValue("id", "role_admin")
	delRec := httptest.NewRecorder()
	h.RequireAuth(h.DeleteRole)(delRec, delReq)
	if delRec.Code != http.StatusForbidden {
		t.Fatalf("delete built-in role: got %d, want 403 — %s", delRec.Code, delRec.Body.String())
	}

	// Seed a target member and a browser session for the owner (who has no custom role with allow_impersonation).
	memberID := uid.New()
	mustExec(t, database, `INSERT INTO users (id, email, name, iana_timezone) VALUES (?, 'member@example.com', 'Member', 'UTC')`, memberID)
	sessID := uid.New()
	exp := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	mustExec(t, database, `INSERT INTO sessions (id, user_id, expires_at) VALUES (?, ?, ?)`, sessID, ownerID, exp)

	impReq := httptest.NewRequest(http.MethodPost, "/v1/admin/impersonate", strings.NewReader(`{"user_id":"`+memberID+`"}`))
	impReq.Header.Set("Content-Type", "application/json")
	impReq.AddCookie(&http.Cookie{Name: "calnode_session", Value: sessID})
	impRec := httptest.NewRecorder()
	h.RequireAuth(h.StartImpersonation)(impRec, impReq)
	if impRec.Code != http.StatusForbidden {
		t.Fatalf("default owner/admin without allow_impersonation role should get 403, got %d — %s", impRec.Code, impRec.Body.String())
	}
}

func TestRBAC_CustomRoleImpersonationLifecycleAndAuditLog(t *testing.T) {
	h, database, apiKey, ownerID := setupWorkspaceWithDB(t)

	memberID := uid.New()
	mustExec(t, database, `INSERT INTO users (id, email, name, iana_timezone) VALUES (?, 'member@example.com', 'Member One', 'UTC')`, memberID)

	// 1. Create custom role with allow_impersonation = true.
	cReq := authReq(http.MethodPost, "/v1/roles", `{"name":"Support Lead","allow_impersonation":true,"can_manage_users":true}`, apiKey)
	cRec := httptest.NewRecorder()
	h.RequireAuth(h.CreateRole)(cRec, cReq)
	if cRec.Code != http.StatusCreated {
		t.Fatalf("CreateRole: got %d — %s", cRec.Code, cRec.Body.String())
	}
	var roleResp struct {
		ID                 string `json:"id"`
		AllowImpersonation bool   `json:"allow_impersonation"`
	}
	_ = json.Unmarshal(cRec.Body.Bytes(), &roleResp)
	if !roleResp.AllowImpersonation {
		t.Fatalf("expected allow_impersonation=true")
	}

	// 2. Assign custom role to owner.
	aReq := authReq(http.MethodPatch, "/v1/users/"+ownerID+"/custom-role", `{"custom_role_id":"`+roleResp.ID+`"}`, apiKey)
	aReq.SetPathValue("id", ownerID)
	aRec := httptest.NewRecorder()
	h.RequireAuth(h.SetUserCustomRole)(aRec, aReq)
	if aRec.Code != http.StatusOK {
		t.Fatalf("SetUserCustomRole: got %d — %s", aRec.Code, aRec.Body.String())
	}

	// 3. Create browser session for owner and start impersonating member.
	sessID := uid.New()
	exp := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	mustExec(t, database, `INSERT INTO sessions (id, user_id, expires_at) VALUES (?, ?, ?)`, sessID, ownerID, exp)

	startReq := httptest.NewRequest(http.MethodPost, "/v1/admin/impersonate", strings.NewReader(`{"user_id":"`+memberID+`"}`))
	startReq.Header.Set("Content-Type", "application/json")
	startReq.AddCookie(&http.Cookie{Name: "calnode_session", Value: sessID})
	startRec := httptest.NewRecorder()
	h.RequireAuth(h.StartImpersonation)(startRec, startReq)
	if startRec.Code != http.StatusOK {
		t.Fatalf("StartImpersonation: got %d — %s", startRec.Code, startRec.Body.String())
	}

	// 4. GET /v1/users/me now returns Member One with impersonating=true.
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

	// 5. Stop impersonation.
	stopReq := httptest.NewRequest(http.MethodPost, "/v1/admin/impersonate/stop", nil)
	stopReq.AddCookie(&http.Cookie{Name: "calnode_session", Value: sessID})
	stopRec := httptest.NewRecorder()
	h.RequireAuth(h.StopImpersonation)(stopRec, stopReq)
	if stopRec.Code != http.StatusOK {
		t.Fatalf("StopImpersonation: got %d — %s", stopRec.Code, stopRec.Body.String())
	}

	// 6. Verify audit log has both start and stop entries.
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
