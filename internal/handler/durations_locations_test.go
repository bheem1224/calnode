package handler_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDurationsAndLocations(t *testing.T) {
	h, db, key, userID := setupWorkspaceWithDB(t)
	seedFullAvailabilityDB(t, db, userID)
	slug, _ := seedEventTypeHTTP(t, h, key)

	patchRec := httptest.NewRecorder()
	patchReq := authReq(http.MethodPatch, "/v1/event-types/"+slug, `{
		"allow_multiple_durations": true,
		"duration_options": [15, 30, 60],
		"location_options": [
			{"type": "in_person", "value": "123 Main St"},
			{"type": "link", "value": "https://meet.example.com/room"}
		]
	}`, key)
	patchReq.SetPathValue("slug", slug)
	h.RequireAuth(h.PatchEventType)(patchRec, patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch durations/locations: %d — %s", patchRec.Code, patchRec.Body.String())
	}

	// Slots endpoint accepts allowed duration and rejects disallowed duration.
	slotOKRec := httptest.NewRecorder()
	slotOKReq := httptest.NewRequest(http.MethodGet, "/v1/event-types/"+slug+"/slots?from=2026-06-20&to=2026-06-20&duration=60", nil)
	slotOKReq.SetPathValue("slug", slug)
	h.GetSlots(slotOKRec, slotOKReq)
	if slotOKRec.Code != http.StatusOK {
		t.Fatalf("slots duration=60: %d — %s", slotOKRec.Code, slotOKRec.Body.String())
	}

	slotBadRec := httptest.NewRecorder()
	slotBadReq := httptest.NewRequest(http.MethodGet, "/v1/event-types/"+slug+"/slots?from=2026-06-20&to=2026-06-20&duration=999", nil)
	slotBadReq.SetPathValue("slug", slug)
	h.GetSlots(slotBadRec, slotBadReq)
	if slotBadRec.Code != http.StatusBadRequest {
		t.Fatalf("slots duration=999: got %d, want 400", slotBadRec.Code)
	}

	// BookPage renders duration pills and location selector.
	pageRec := httptest.NewRecorder()
	pageReq := httptest.NewRequest(http.MethodGet, "/book/"+slug, nil)
	pageReq.SetPathValue("slug", slug)
	h.BookPage(pageRec, pageReq)
	if pageRec.Code != http.StatusOK {
		t.Fatalf("BookPage: %d", pageRec.Code)
	}
	body := pageRec.Body.String()
	if !strings.Contains(body, `id="duration-picker"`) || !strings.Contains(body, `id="f-location-select"`) {
		t.Errorf("BookPage missing duration-picker or f-location-select")
	}

	// Create booking with duration_minutes=60 and selected_location=link.
	bBody := fmt.Sprintf(`{
		"event_type_slug": %q,
		"start_at": "2026-06-20T09:00:00Z",
		"name": "Alice",
		"email": "alice@example.com",
		"duration_minutes": 60,
		"selected_location": {"type": "link", "value": "https://meet.example.com/room"}
	}`, slug)
	bRec := httptest.NewRecorder()
	bReq := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(bBody))
	bReq.Header.Set("Content-Type", "application/json")
	h.CreateBooking(bRec, bReq)
	if bRec.Code != http.StatusCreated {
		t.Fatalf("create booking with duration/location: %d — %s", bRec.Code, bRec.Body.String())
	}
	resp := mustCreated(t, bRec, "booking")
	if got := mustString(t, resp, "end_at", "booking"); got != "2026-06-20T10:00:00Z" {
		t.Errorf("end_at = %q; want 2026-06-20T10:00:00Z", got)
	}
	if got := mustString(t, resp, "location_type", "booking"); got != "link" {
		t.Errorf("location_type = %q; want link", got)
	}
	if got := mustString(t, resp, "location_value", "booking"); got != "https://meet.example.com/room" {
		t.Errorf("location_value = %q; want https://meet.example.com/room", got)
	}
}
