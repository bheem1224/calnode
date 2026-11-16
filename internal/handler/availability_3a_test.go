package handler_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/uid"
	"github.com/calnode/calnode/internal/worker"
)

func TestScheduledTimezoneChange_WorkerApplies(t *testing.T) {
	h, database, apiKey, userID := setupWorkspaceWithDB(t)

	pastEff := time.Now().UTC().Add(-2 * time.Minute).Format(time.RFC3339)
	patchReq := authReq(http.MethodPatch, "/v1/users/me", `{"scheduled_timezone":"America/Los_Angeles","timezone_effective_at":"`+pastEff+`"}`, apiKey)
	patchRec := httptest.NewRecorder()
	h.RequireAuth(h.PatchMe)(patchRec, patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("PatchMe: got %d — %s", patchRec.Code, patchRec.Body.String())
	}

	w := worker.New(database, nil, slog.Default())
	w.Poll(context.Background())

	var tz, stz, eff string
	if err := database.QueryRow(`SELECT iana_timezone, scheduled_timezone, timezone_effective_at FROM users WHERE id = ?`, userID).
		Scan(&tz, &stz, &eff); err != nil {
		t.Fatalf("query user: %v", err)
	}
	if tz != "America/Los_Angeles" || stz != "" || eff != "" {
		t.Fatalf("expected tz=America/Los_Angeles and cleared schedule, got tz=%q stz=%q eff=%q", tz, stz, eff)
	}
}

func TestMultipleAvailabilitySchedules_And_OOO(t *testing.T) {
	h, _, apiKey, _ := setupWorkspaceWithDB(t)

	// Create a named schedule.
	sReq := authReq(http.MethodPost, "/v1/availability-schedules", `{"name":"Evening Hours","timezone":"UTC"}`, apiKey)
	sRec := httptest.NewRecorder()
	h.RequireAuth(h.CreateAvailabilitySchedule)(sRec, sReq)
	if sRec.Code != http.StatusCreated {
		t.Fatalf("CreateAvailabilitySchedule: got %d — %s", sRec.Code, sRec.Body.String())
	}
	var sched struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(sRec.Body.Bytes(), &sched)

	// Add a Monday rule (18:00-20:00) on that schedule.
	rReq := authReq(http.MethodPost, "/v1/availability-rules", `{"schedule_id":"`+sched.ID+`","day_of_week":1,"start_time":"18:00","end_time":"20:00"}`, apiKey)
	rRec := httptest.NewRecorder()
	h.RequireAuth(h.CreateAvailabilityRule)(rRec, rReq)
	if rRec.Code != http.StatusCreated {
		t.Fatalf("CreateAvailabilityRule: got %d — %s", rRec.Code, rRec.Body.String())
	}

	// Create an event type and assign schedule_id.
	etReq := authReq(http.MethodPost, "/v1/event-types", `{"name":"Evening Call","slug":"evening-call","duration_minutes":30,"location_type":"link","location_value":"https://meet.example.com/e"}`, apiKey)
	etRec := httptest.NewRecorder()
	h.RequireAuth(h.CreateEventType)(etRec, etReq)
	if etRec.Code != http.StatusCreated {
		t.Fatalf("CreateEventType: got %d — %s", etRec.Code, etRec.Body.String())
	}
	pReq := authReq(http.MethodPatch, "/v1/event-types/evening-call", `{"schedule_id":"`+sched.ID+`"}`, apiKey)
	pReq.SetPathValue("slug", "evening-call")
	pRec := httptest.NewRecorder()
	h.RequireAuth(h.PatchEventType)(pRec, pReq)
	if pRec.Code != http.StatusOK {
		t.Fatalf("PatchEventType: got %d — %s", pRec.Code, pRec.Body.String())
	}

	// Find next Monday.
	now := time.Now().UTC()
	daysUntilMon := (int(time.Monday) - int(now.Weekday()) + 7) % 7
	if daysUntilMon == 0 {
		daysUntilMon = 7
	}
	nextMon := now.AddDate(0, 0, daysUntilMon).Format("2006-01-02")

	// Check slots before OOO: should have 18:00 UTC slots.
	slReq := httptest.NewRequest(http.MethodGet, "/v1/event-types/evening-call/slots?from="+nextMon+"&to="+nextMon+"&tz=UTC", nil)
	slReq.SetPathValue("slug", "evening-call")
	slRec := httptest.NewRecorder()
	h.GetSlots(slRec, slReq)
	if slRec.Code != http.StatusOK {
		t.Fatalf("GetSlots: got %d — %s", slRec.Code, slRec.Body.String())
	}
	var slResp struct {
		Slots []struct {
			Start string `json:"start"`
		} `json:"slots"`
	}
	_ = json.Unmarshal(slRec.Body.Bytes(), &slResp)
	if len(slResp.Slots) == 0 {
		t.Fatalf("expected evening slots on %s, got 0", nextMon)
	}

	// Block nextMon with OOO entry.
	oooReq := authReq(http.MethodPost, "/v1/ooo", `{"dates":["`+nextMon+`"],"reason":"holiday","public_note":"Off for holiday"}`, apiKey)
	oooRec := httptest.NewRecorder()
	h.RequireAuth(h.CreateOOOEntry)(oooRec, oooReq)
	if oooRec.Code != http.StatusCreated {
		t.Fatalf("CreateOOOEntry: got %d — %s", oooRec.Code, oooRec.Body.String())
	}

	slReq2 := httptest.NewRequest(http.MethodGet, "/v1/event-types/evening-call/slots?from="+nextMon+"&to="+nextMon+"&tz=UTC", nil)
	slReq2.SetPathValue("slug", "evening-call")
	slRec2 := httptest.NewRecorder()
	h.GetSlots(slRec2, slReq2)
	var slResp2 struct {
		Slots []any `json:"slots"`
	}
	_ = json.Unmarshal(slRec2.Body.Bytes(), &slResp2)
	if len(slResp2.Slots) != 0 {
		t.Fatalf("expected 0 slots on OOO date %s, got %d", nextMon, len(slResp2.Slots))
	}
}

func TestDynamicGroupLinks_IntersectionAndBooking(t *testing.T) {
	h, database, _, aliceID := setupWorkspaceWithDB(t)

	bobID := uid.New()
	mustExec(t, database, `UPDATE users SET handle = 'alice' WHERE id = ?`, aliceID)
	mustExec(t, database, `INSERT INTO users (id, email, name, handle, iana_timezone) VALUES (?, 'bob@example.com', 'Bob', 'bob', 'UTC')`, bobID)

	// Alice: Monday 09:00-12:00 UTC; Bob: Monday 11:00-15:00 UTC => intersection is 11:00-12:00 UTC.
	mustExec(t, database, `INSERT INTO availability_rules (id, user_id, day_of_week, start_time, end_time) VALUES ('r-a', ?, 1, '09:00', '12:00')`, aliceID)
	mustExec(t, database, `INSERT INTO availability_rules (id, user_id, day_of_week, start_time, end_time) VALUES ('r-b', ?, 1, '11:00', '15:00')`, bobID)

	now := time.Now().UTC()
	daysUntilMon := (int(time.Monday) - int(now.Weekday()) + 7) % 7
	if daysUntilMon == 0 {
		daysUntilMon = 7
	}
	nextMon := now.AddDate(0, 0, daysUntilMon).Format("2006-01-02")

	req := httptest.NewRequest(http.MethodGet, "/v1/public/group/alice+bob/slots?from="+nextMon+"&to="+nextMon+"&duration=30&tz=UTC", nil)
	req.SetPathValue("handles", "alice+bob")
	rec := httptest.NewRecorder()
	h.GetDynamicGroupSlots(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GetDynamicGroupSlots: got %d — %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Slots []struct {
			Start time.Time `json:"start"`
		} `json:"slots"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Slots) != 2 {
		t.Fatalf("expected 2 intersection slots (11:00 and 11:30), got %d: %s", len(resp.Slots), rec.Body.String())
	}
	if resp.Slots[0].Start.UTC().Hour() != 11 || resp.Slots[0].Start.UTC().Minute() != 0 {
		t.Fatalf("expected first slot at 11:00 UTC, got %v", resp.Slots[0].Start.UTC())
	}

	// Book the 11:00 UTC slot.
	startStr := resp.Slots[0].Start.UTC().Format(time.RFC3339)
	bReq := httptest.NewRequest(http.MethodPost, "/v1/public/group/alice+bob/bookings",
		strings.NewReader(`{"start_at":"`+startStr+`","duration_minutes":30,"attendee_name":"Client","attendee_email":"client@example.com","attendee_tz":"UTC"}`))
	bReq.SetPathValue("handles", "alice+bob")
	bRec := httptest.NewRecorder()
	h.CreateDynamicGroupBooking(bRec, bReq)
	if bRec.Code != http.StatusCreated {
		t.Fatalf("CreateDynamicGroupBooking: got %d — %s", bRec.Code, bRec.Body.String())
	}

	var bOut struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(bRec.Body.Bytes(), &bOut)
	var hostCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM booking_hosts WHERE booking_id = ?`, bOut.ID).Scan(&hostCount); err != nil {
		t.Fatalf("count booking_hosts: %v", err)
	}
	if hostCount != 2 {
		t.Fatalf("expected 2 booking_hosts for alice+bob, got %d", hostCount)
	}
}
