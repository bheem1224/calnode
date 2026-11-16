package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestManagePolicies_cancelAndReschedule(t *testing.T) {
	h, db, apiKey, userID := setupWorkspaceWithDB(t)
	seedFullAvailabilityDB(t, db, userID)

	// Create event type.
	etRec := httptest.NewRecorder()
	etReq := authReq(http.MethodPost, "/v1/event-types", `{
		"slug": "policy-et",
		"name": "Policy ET",
		"duration_minutes": 30,
		"max_future_days": 0,
		"location_type": "in_person"
	}`, apiKey)
	h.RequireAuth(h.CreateEventType)(etRec, etReq)
	if etRec.Code != http.StatusCreated {
		t.Fatalf("create event type: %d — %s", etRec.Code, etRec.Body.String())
	}

	// Book a slot 2 days from now at 12:00 UTC.
	d := time.Now().UTC().AddDate(0, 0, 2)
	slotStart := time.Date(d.Year(), d.Month(), d.Day(), 12, 0, 0, 0, time.UTC)
	bookRec := httptest.NewRecorder()
	bookReq := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(`{
		"event_type_slug": "policy-et",
		"start_at": "`+slotStart.Format(time.RFC3339)+`",
		"name": "Alice",
		"email": "alice@example.com",
		"timezone": "UTC"
	}`))
	bookReq.Header.Set("Content-Type", "application/json")
	h.CreateBooking(bookRec, bookReq)
	if bookRec.Code != http.StatusCreated {
		t.Fatalf("create booking: %d — %s", bookRec.Code, bookRec.Body.String())
	}
	var bk struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(bookRec.Body.Bytes(), &bk)

	manageToken := issueTestToken(t, db, bk.ID)

	// 1. require_cancel_reason = "required" -> empty reason returns 400.
	patchRec := httptest.NewRecorder()
	patchReq := authReq(http.MethodPatch, "/v1/event-types/policy-et", `{"require_cancel_reason":"required"}`, apiKey)
	patchReq.SetPathValue("slug", "policy-et")
	h.RequireAuth(h.PatchEventType)(patchRec, patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch require_cancel_reason: %d — %s", patchRec.Code, patchRec.Body.String())
	}

	cRec := httptest.NewRecorder()
	cReq := httptest.NewRequest(http.MethodPost, "/manage/"+manageToken+"/cancel", bytes.NewBufferString(`{"reason":"   "}`))
	cReq.SetPathValue("token", manageToken)
	h.CancelByToken(cRec, cReq)
	if cRec.Code != http.StatusBadRequest {
		t.Errorf("cancel without required reason: status = %d; want 400", cRec.Code)
	}

	// 2. disable_cancel & disable_reschedule -> ManagePage omits buttons, endpoints return 403.
	patchRec2 := httptest.NewRecorder()
	patchReq2 := authReq(http.MethodPatch, "/v1/event-types/policy-et", `{"disable_cancel":true,"disable_reschedule":true}`, apiKey)
	patchReq2.SetPathValue("slug", "policy-et")
	h.RequireAuth(h.PatchEventType)(patchRec2, patchReq2)
	if patchRec2.Code != http.StatusOK {
		t.Fatalf("patch disable flags: %d — %s", patchRec2.Code, patchRec2.Body.String())
	}

	pageRec := httptest.NewRecorder()
	pageReq := httptest.NewRequest(http.MethodGet, "/manage/"+manageToken, nil)
	pageReq.SetPathValue("token", manageToken)
	h.ManagePage(pageRec, pageReq)
	if pageRec.Code != http.StatusOK {
		t.Fatalf("manage page: %d", pageRec.Code)
	}
	body := pageRec.Body.String()
	if strings.Contains(body, `id="cancel-btn"`) || strings.Contains(body, `id="reschedule-btn"`) {
		t.Errorf("manage page should omit disabled cancel/reschedule buttons")
	}

	cRec2 := httptest.NewRecorder()
	cReq2 := httptest.NewRequest(http.MethodPost, "/manage/"+manageToken+"/cancel", bytes.NewBufferString(`{"reason":"can't make it"}`))
	cReq2.SetPathValue("token", manageToken)
	h.CancelByToken(cRec2, cReq2)
	if cRec2.Code != http.StatusForbidden {
		t.Errorf("disabled cancel: status = %d; want 403", cRec2.Code)
	}

	newStart := slotStart.Add(24 * time.Hour)
	rRec := httptest.NewRecorder()
	rReq := httptest.NewRequest(http.MethodPost, "/manage/"+manageToken+"/reschedule", bytes.NewBufferString(`{"start_at":"`+newStart.Format(time.RFC3339)+`"}`))
	rReq.SetPathValue("token", manageToken)
	h.RescheduleByToken(rRec, rReq)
	if rRec.Code != http.StatusForbidden {
		t.Errorf("disabled reschedule: status = %d; want 403", rRec.Code)
	}

	// 3. Past booking reschedule gated by allow_reschedule_past.
	patchRec3 := httptest.NewRecorder()
	patchReq3 := authReq(http.MethodPatch, "/v1/event-types/policy-et", `{"disable_reschedule":false,"allow_reschedule_past":false}`, apiKey)
	patchReq3.SetPathValue("slug", "policy-et")
	h.RequireAuth(h.PatchEventType)(patchRec3, patchReq3)

	pastStart := "2026-05-31T09:00:00Z"
	pastEnd := "2026-05-31T09:30:00Z"
	if _, err := db.Exec(`UPDATE bookings SET start_at = ?, end_at = ? WHERE id = ?`, pastStart, pastEnd, bk.ID); err != nil {
		t.Fatalf("move booking to past: %v", err)
	}

	rRec2 := httptest.NewRecorder()
	rReq2 := httptest.NewRequest(http.MethodPost, "/manage/"+manageToken+"/reschedule", bytes.NewBufferString(`{"start_at":"`+newStart.Format(time.RFC3339)+`"}`))
	rReq2.SetPathValue("token", manageToken)
	h.RescheduleByToken(rRec2, rReq2)
	if rRec2.Code != http.StatusForbidden {
		t.Errorf("past reschedule when disallowed: status = %d; want 403", rRec2.Code)
	}

	patchRec4 := httptest.NewRecorder()
	patchReq4 := authReq(http.MethodPatch, "/v1/event-types/policy-et", `{"allow_reschedule_past":true}`, apiKey)
	patchReq4.SetPathValue("slug", "policy-et")
	h.RequireAuth(h.PatchEventType)(patchRec4, patchReq4)

	rRec3 := httptest.NewRecorder()
	rReq3 := httptest.NewRequest(http.MethodPost, "/manage/"+manageToken+"/reschedule", bytes.NewBufferString(`{"start_at":"`+newStart.Format(time.RFC3339)+`"}`))
	rReq3.SetPathValue("token", manageToken)
	h.RescheduleByToken(rRec3, rReq3)
	if rRec3.Code != http.StatusOK {
		t.Errorf("past reschedule when allowed: status = %d; want 200 — %s", rRec3.Code, rRec3.Body.String())
	}
}
