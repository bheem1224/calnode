package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/booking"
)

func TestLimitsAndFutureModes_slotsAndBookingEnforcement(t *testing.T) {
	h, db, apiKey, userID := setupWorkspaceWithDB(t)
	seedFullAvailabilityDB(t, db, userID)

	// Create 60-min event type.
	etRec := httptest.NewRecorder()
	etReq := authReq(http.MethodPost, "/v1/event-types", `{
		"slug": "limits-et",
		"name": "Limits ET",
		"duration_minutes": 60,
		"max_future_days": 0,
		"location_type": "in_person"
	}`, apiKey)
	h.RequireAuth(h.CreateEventType)(etRec, etReq)
	if etRec.Code != http.StatusCreated {
		t.Fatalf("create event type: %d — %s", etRec.Code, etRec.Body.String())
	}

	d := time.Now().UTC().AddDate(0, 0, 5)
	dayStr := d.Format("2006-01-02")

	// 1. show_first_slot_only -> /v1/slots returns exactly 1 slot for that day.
	pRec := httptest.NewRecorder()
	pReq := authReq(http.MethodPatch, "/v1/event-types/limits-et", `{"show_first_slot_only":true}`, apiKey)
	pReq.SetPathValue("slug", "limits-et")
	h.RequireAuth(h.PatchEventType)(pRec, pReq)
	if pRec.Code != http.StatusOK {
		t.Fatalf("patch show_first_slot_only: %d — %s", pRec.Code, pRec.Body.String())
	}

	sRec := httptest.NewRecorder()
	sReq := httptest.NewRequest(http.MethodGet, "/v1/event-types/limits-et/slots?from="+dayStr+"&to="+dayStr+"&tz=UTC", nil)
	sReq.SetPathValue("slug", "limits-et")
	h.GetSlots(sRec, sReq)
	if sRec.Code != http.StatusOK {
		t.Fatalf("get slots: %d — %s", sRec.Code, sRec.Body.String())
	}
	var slotsResp struct {
		Slots []struct {
			Start string `json:"start"`
		} `json:"slots"`
	}
	_ = json.Unmarshal(sRec.Body.Bytes(), &slotsResp)
	if len(slotsResp.Slots) != 1 {
		t.Fatalf("show_first_slot_only slots = %d; want 1", len(slotsResp.Slots))
	}

	// 2. max_duration_per_day = 60 -> 1st 60m booking succeeds, 2nd 60m booking on same day -> 409.
	pRec2 := httptest.NewRecorder()
	pReq2 := authReq(http.MethodPatch, "/v1/event-types/limits-et", `{"show_first_slot_only":false,"max_duration_per_day":60}`, apiKey)
	pReq2.SetPathValue("slug", "limits-et")
	h.RequireAuth(h.PatchEventType)(pRec2, pReq2)
	if pRec2.Code != http.StatusOK {
		t.Fatalf("patch max_duration_per_day: %d — %s", pRec2.Code, pRec2.Body.String())
	}

	slot1 := time.Date(d.Year(), d.Month(), d.Day(), 10, 0, 0, 0, time.UTC)
	slot2 := time.Date(d.Year(), d.Month(), d.Day(), 14, 0, 0, 0, time.UTC)
	_ = createBookingViaHTTP(t, h, "limits-et", slot1.Format(time.RFC3339))

	bRec2 := httptest.NewRecorder()
	bReq2 := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(`{
		"event_type_slug": "limits-et",
		"start_at": "`+slot2.Format(time.RFC3339)+`",
		"name": "Bob",
		"email": "bob@example.com",
		"timezone": "UTC"
	}`))
	bReq2.Header.Set("Content-Type", "application/json")
	h.CreateBooking(bRec2, bReq2)
	if bRec2.Code != http.StatusConflict {
		t.Errorf("exceeding max_duration_per_day: status = %d; want 409", bRec2.Code)
	}

	// 3. future_limit_mode = "date_range" -> outside range rejected with 409.
	inRange := time.Now().UTC().AddDate(0, 0, 10).Format("2006-01-02")
	pRec3 := httptest.NewRecorder()
	pReq3 := authReq(http.MethodPatch, "/v1/event-types/limits-et", `{
		"max_duration_per_day": null,
		"future_limit_mode": "date_range",
		"future_range_start": "`+inRange+`",
		"future_range_end": "`+inRange+`"
	}`, apiKey)
	pReq3.SetPathValue("slug", "limits-et")
	h.RequireAuth(h.PatchEventType)(pRec3, pReq3)
	if pRec3.Code != http.StatusOK {
		t.Fatalf("patch date_range: %d — %s", pRec3.Code, pRec3.Body.String())
	}

	bRec3 := httptest.NewRecorder()
	bReq3 := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(`{
		"event_type_slug": "limits-et",
		"start_at": "`+slot2.Format(time.RFC3339)+`",
		"name": "Carol",
		"email": "carol@example.com",
		"timezone": "UTC"
	}`))
	bReq3.Header.Set("Content-Type", "application/json")
	h.CreateBooking(bRec3, bReq3)
	if bRec3.Code != http.StatusConflict {
		t.Errorf("outside date_range: status = %d; want 409", bRec3.Code)
	}

	// 4. WithinFutureLimit business_days: Friday + 1 business day = Monday (allows Monday, blocks Tuesday).
	friday := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	monday := time.Date(2026, 11, 23, 15, 0, 0, 0, time.UTC)
	tuesday := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	if !booking.WithinFutureLimit(friday, monday, time.UTC, "business_days", 1, "", "") {
		t.Errorf("1 business day from Friday should allow Monday")
	}
	if booking.WithinFutureLimit(friday, tuesday, time.UTC, "business_days", 1, "", "") {
		t.Errorf("1 business day from Friday should block Tuesday")
	}
}
