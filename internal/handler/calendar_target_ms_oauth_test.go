package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calnode/calnode/internal/calendar"
)

func TestCalendarTarget_PatchAndListConnectedCalendars(t *testing.T) {
	h, database, apiKey, userID := setupWorkspaceWithDB(t)

	// Seed two calendar connections and calendars.
	mustExec(t, database, `
		INSERT INTO calendar_connections (id, user_id, provider, account_email, access_token_enc, calendar_id)
		VALUES
		  ('conn-1', ?, 'google', 'work@example.com', 'enc1', 'cal-work-1'),
		  ('conn-2', ?, 'microsoft', 'personal@outlook.com', 'enc2', 'cal-personal-2')`,
		userID, userID)
	mustExec(t, database, `
		INSERT INTO connection_calendars (id, user_id, calendar_id, name, provider, account_email, is_destination)
		VALUES
		  ('cc-1', ?, 'cal-work-1', 'Work Primary', 'google', 'work@example.com', 1),
		  ('cc-2', ?, 'cal-personal-2', 'Personal Outlook', 'microsoft', 'personal@outlook.com', 0)`,
		userID, userID)

	// GET /v1/calendar/calendars should return both connected calendars.
	req := authReq(http.MethodGet, "/v1/calendar/calendars", "", apiKey)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ListConnectedCalendars)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ListConnectedCalendars: got %d — %s", rec.Code, rec.Body.String())
	}

	var listResp struct {
		Items []struct {
			CalendarID    string `json:"calendar_id"`
			Name          string `json:"name"`
			Provider      string `json:"provider"`
			AccountEmail  string `json:"account_email"`
			IsDestination bool   `json:"is_destination"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode ListConnectedCalendars: %v", err)
	}
	if len(listResp.Items) != 2 {
		t.Fatalf("expected 2 connected calendars, got %d", len(listResp.Items))
	}

	// Create an event type and PATCH destination_calendar_id.
	createBody := `{"name":"Target Test","slug":"target-test","duration_minutes":30,"location_type":"link","location_value":"https://meet.example.com/room"}`
	createReq := authReq(http.MethodPost, "/v1/event-types", createBody, apiKey)
	createRec := httptest.NewRecorder()
	h.RequireAuth(h.CreateEventType)(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("CreateEventType: got %d — %s", createRec.Code, createRec.Body.String())
	}

	patchReq := authReq(http.MethodPatch, "/v1/event-types/target-test", `{"destination_calendar_id":"cal-personal-2"}`, apiKey)
	patchReq.SetPathValue("slug", "target-test")
	patchRec := httptest.NewRecorder()
	h.RequireAuth(h.PatchEventType)(patchRec, patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("PatchEventType: got %d — %s", patchRec.Code, patchRec.Body.String())
	}

	var etResp struct {
		DestinationCalendarID string `json:"destination_calendar_id"`
	}
	if err := json.Unmarshal(patchRec.Body.Bytes(), &etResp); err != nil {
		t.Fatalf("decode PatchEventType: %v", err)
	}
	if etResp.DestinationCalendarID != "cal-personal-2" {
		t.Fatalf("expected destination_calendar_id 'cal-personal-2', got %q", etResp.DestinationCalendarID)
	}

	// Verify clearing destination_calendar_id back to "".
	clearReq := authReq(http.MethodPatch, "/v1/event-types/target-test", `{"destination_calendar_id":""}`, apiKey)
	clearReq.SetPathValue("slug", "target-test")
	clearRec := httptest.NewRecorder()
	h.RequireAuth(h.PatchEventType)(clearRec, clearReq)
	if clearRec.Code != http.StatusOK {
		t.Fatalf("PatchEventType clear: got %d — %s", clearRec.Code, clearRec.Body.String())
	}
	var clearResp struct {
		DestinationCalendarID string `json:"destination_calendar_id"`
	}
	if err := json.Unmarshal(clearRec.Body.Bytes(), &clearResp); err != nil {
		t.Fatalf("decode PatchEventType clear: %v", err)
	}
	if clearResp.DestinationCalendarID != "" {
		t.Fatalf("expected destination_calendar_id cleared to '', got %q", clearResp.DestinationCalendarID)
	}
}

func TestMicrosoftSettings_TenantPinnedSSOWithPersonalCalendarOAuth(t *testing.T) {
	h, database, apiKey, _ := setupWorkspaceWithDB(t)
	h.SetEncKey("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	h.SetBaseURL("https://cal.example.com")
	h.SetCalendar(calendar.NewService(database))

	putBody := `{
		"client_id": "sso-client-123",
		"client_secret": "sso-secret-xyz",
		"tenant_id": "contoso-tenant",
		"allowed_signup_domains": "contoso.com",
		"allow_personal_calendars": true,
		"cal_client_id": "cal-client-456",
		"cal_client_secret": "cal-secret-abc"
	}`
	putReq := authReq(http.MethodPut, "/v1/settings/microsoft", putBody, apiKey)
	putRec := httptest.NewRecorder()
	h.RequireAuth(h.PutMicrosoftSettings)(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("PutMicrosoftSettings: got %d — %s", putRec.Code, putRec.Body.String())
	}

	getReq := authReq(http.MethodGet, "/v1/settings/microsoft", "", apiKey)
	getRec := httptest.NewRecorder()
	h.RequireAuth(h.GetMicrosoftSettings)(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GetMicrosoftSettings: got %d — %s", getRec.Code, getRec.Body.String())
	}

	var msResp struct {
		ClientID               string `json:"client_id"`
		ClientSecretSet        bool   `json:"client_secret_set"`
		TenantID               string `json:"tenant_id"`
		AllowPersonalCalendars bool   `json:"allow_personal_calendars"`
		CalClientID            string `json:"cal_client_id"`
		CalClientSecretSet     bool   `json:"cal_client_secret_set"`
		Configured             bool   `json:"configured"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &msResp); err != nil {
		t.Fatalf("decode GetMicrosoftSettings: %v", err)
	}
	if !msResp.AllowPersonalCalendars || msResp.CalClientID != "cal-client-456" || !msResp.CalClientSecretSet {
		t.Fatalf("unexpected Microsoft settings response: %+v", msResp)
	}
	if msResp.TenantID != "contoso-tenant" {
		t.Fatalf("expected tenant_id 'contoso-tenant', got %q", msResp.TenantID)
	}

	// Verify SSO login remains pinned to contoso-tenant with sso-client-123.
	ssoReq := httptest.NewRequest(http.MethodGet, "/v1/auth/microsoft/login", nil)
	ssoRec := httptest.NewRecorder()
	h.LoginMicrosoft(ssoRec, ssoReq)
	if ssoRec.Code != http.StatusFound && ssoRec.Code != http.StatusTemporaryRedirect && ssoRec.Code != http.StatusOK {
		t.Fatalf("LoginMicrosoft: got %d — %s", ssoRec.Code, ssoRec.Body.String())
	}
	ssoTarget := ssoRec.Header().Get("Location") + " " + ssoRec.Body.String()
	if !strings.Contains(ssoTarget, "login.microsoftonline.com/contoso-tenant/") {
		t.Fatalf("expected SSO to stay pinned to contoso-tenant, got %q", ssoTarget)
	}
	if !strings.Contains(ssoTarget, "client_id=sso-client-123") {
		t.Fatalf("expected SSO to use sso-client-123, got %q", ssoTarget)
	}

	// Verify Calendar connect uses 'common' with cal-client-456.
	calReq := authReq(http.MethodGet, "/v1/calendar/connect?provider=microsoft", "", apiKey)
	calRec := httptest.NewRecorder()
	h.RequireAuth(h.ConnectCalendar)(calRec, calReq)
	if calRec.Code != http.StatusFound && calRec.Code != http.StatusTemporaryRedirect && calRec.Code != http.StatusOK {
		t.Fatalf("ConnectCalendar: got %d — %s", calRec.Code, calRec.Body.String())
	}
	calAuthURL := calRec.Header().Get("Location")
	if calAuthURL == "" {
		var calResp struct {
			AuthURL string `json:"auth_url"`
		}
		if err := json.Unmarshal(calRec.Body.Bytes(), &calResp); err != nil {
			t.Fatalf("decode ConnectCalendar: %v", err)
		}
		calAuthURL = calResp.AuthURL
	}
	if !strings.Contains(calAuthURL, "login.microsoftonline.com/common/") {
		t.Fatalf("expected calendar OAuth to use 'common' tenant, got %q", calAuthURL)
	}
	if !strings.Contains(calAuthURL, "client_id=cal-client-456") {
		t.Fatalf("expected calendar OAuth to use cal-client-456, got %q", calAuthURL)
	}
}
