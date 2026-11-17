package handler_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/calendar"
	"github.com/calnode/calnode/internal/handler"
	"github.com/calnode/calnode/internal/slots"
)

func previewCall(h *handler.Handler, key, slug, query, draft string, page bool) *httptest.ResponseRecorder {
	path := "/v1/event-types/" + slug + "/preview"
	fn := h.PreviewBookingPage
	if !page {
		path += "/slots"
		fn = h.PreviewBookingSlots
	}
	req := authReq(http.MethodPost, path+query, draft, key)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.RequireAuth(fn)(rec, req)
	return rec
}

func TestBookingPreviewUsesDraftSlotsWithoutSaving(t *testing.T) {
	h, database, key, owner := setupWorkspaceWithDB(t)
	slug, id := seedEventTypeHTTP(t, h, key)
	day := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, 7)
	mustExec(t, database, `DELETE FROM availability_rules WHERE user_id = ?`, owner)
	seedAvailabilityWindow(t, h, key, int(day.Weekday()), "09:00", "12:00")
	busy := day.Add(10 * time.Hour)
	provider := &conflictCalendar{name: "google", busy: map[string][]slots.Interval{owner: {{Start: busy, End: busy.Add(30 * time.Minute)}}}}
	svc := calendar.NewService(database)
	svc.Register(provider)
	h.SetCalendar(svc)
	query := "?from=" + day.Format("2006-01-02") + "&to=" + day.Format("2006-01-02") + "&tz=UTC"
	get := func(event string) []string {
		t.Helper()
		rec := previewCall(h, key, slug, query, `{"event_type":`+event+`}`, false)
		mustStatus(t, rec, http.StatusOK, "preview slots")
		var result struct {
			Slots []struct {
				Start string `json:"start"`
			} `json:"slots"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		var starts []string
		for _, slot := range result.Slots {
			starts = append(starts, slot.Start[11:16])
		}
		return starts
	}
	if got := strings.Join(get(`{"slot_interval_minutes":30}`), ","); got != "09:00,09:30,10:30,11:00,11:30" {
		t.Fatalf("saved spacing and live FreeBusy: %s", got)
	}
	if got := strings.Join(get(`{"slot_interval_minutes":45}`), ","); got != "09:00,10:30,11:15" {
		t.Fatalf("draft 45-minute spacing: %s", got)
	}
	if got := strings.Join(get(`{"buffer_before_minutes":30,"buffer_after_minutes":30}`), ","); got != "09:00,11:00,11:30" {
		t.Fatalf("draft buffers around live conflict: %s", got)
	}
	if got := get(`{"min_notice_minutes":20160}`); len(got) != 0 {
		t.Fatalf("draft minimum notice not applied: %v", got)
	}
	if provider.calls.Load() < 4 {
		t.Fatal("preview did not check live calendars")
	}
	var interval, before, after, bookings, tokens int
	if err := database.QueryRow(`SELECT slot_interval_minutes, buffer_before_minutes, buffer_after_minutes FROM event_types WHERE id = ?`, id).Scan(&interval, &before, &after); err != nil {
		t.Fatal(err)
	}
	_ = database.QueryRow(`SELECT COUNT(*) FROM bookings`).Scan(&bookings)
	_ = database.QueryRow(`SELECT COUNT(*) FROM single_use_tokens`).Scan(&tokens)
	if interval != 30 || before != 0 || after != 0 || bookings != 0 || tokens != 0 {
		t.Fatalf("preview mutated saved state: interval=%d buffers=%d/%d bookings=%d tokens=%d", interval, before, after, bookings, tokens)
	}
}

func TestBookingPreviewRendersPublicTemplateAndDraftForm(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)
	slug, id := seedEventTypeHTTP(t, h, key)
	mustExec(t, database, `UPDATE event_types SET is_active = 0, is_public = 0 WHERE id = ?`, id)
	draft := `{"event_type":{"name":"Unsaved preview name","duration_minutes":45,"booking_form_config":"[{\"key\":\"email\"},{\"key\":\"name\"},{\"key\":\"notes\",\"hidden\":true}]"},"mode":"form","questions":[{"id":"draft-q","label":"Unsaved question","type":"text","required":true}]}`
	// The native iframe form posts a draft without placing private form content in a URL.
	path := "/v1/event-types/" + slug + "/preview"
	req := authReq(http.MethodPost, path, url.Values{"draft": {draft}}.Encode(), key)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.PreviewBookingPage)(rec, req)
	mustStatus(t, rec, http.StatusOK, "preview page")
	for _, expected := range []string{"Unsaved preview name", "Unsaved question", "45 min", `id="cal"`, `id="booking-form"`, `id="confirm-view"`, `id="submit-btn" class="btn-primary" disabled`, `if (PREVIEW) return;`, `"mode":"form"`} {
		if !strings.Contains(rec.Body.String(), expected) {
			t.Errorf("public preview missing %q", expected)
		}
	}
	if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("X-Frame-Options") != "SAMEORIGIN" {
		t.Fatalf("unsafe preview headers: %v", rec.Header())
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "/preview/slots") || !strings.Contains(csp, "form-action 'none'") || strings.Contains(csp, "connect-src 'self'") {
		t.Fatalf("preview can reach write endpoints: %s", csp)
	}
	if strings.Contains(rec.Body.String(), `class="fp-theme-switcher"`) || strings.Contains(rec.Body.String(), `class="asst-form"`) {
		t.Fatal("preview leaked public chrome / assistant")
	}
	var saved string
	if err := database.QueryRow(`SELECT name FROM event_types WHERE id = ?`, id).Scan(&saved); err != nil || saved == "Unsaved preview name" {
		t.Fatal("preview saved the draft")
	}
	page := httptest.NewRecorder()
	publicReq := httptest.NewRequest(http.MethodGet, "/book/"+slug, nil)
	publicReq.SetPathValue("slug", slug)
	h.BookPage(page, publicReq)
	mustStatus(t, page, http.StatusNotFound, "inactive public page remains unavailable")
}

func TestBookingPreviewAuthorizationAndValidation(t *testing.T) {
	ws := setupAdminWorkspace(t)
	// Preview must accept durations the event editor API already supports.
	mustStatus(t, previewCall(ws.h, ws.ownerKey, ws.slug, "", `{"event_type":{"duration_minutes":2880,"slot_interval_minutes":2880}}`, true), http.StatusOK, "multi-day preview")
	for _, page := range []bool{true, false} {
		mustStatus(t, previewCall(ws.h, "", ws.slug, "", `{}`, page), http.StatusUnauthorized, "unauthenticated preview")
		mustStatus(t, previewCall(ws.h, ws.otherKey, ws.slug, "", `{}`, page), http.StatusNotFound, "another member's preview")
		for _, body := range []string{`{"event_type":{"slot_interval_minutes":0}}`, `{"event_type":{"buffer_before_minutes":-5}}`, `{"mode":"book"}`} {
			mustStatus(t, previewCall(ws.h, ws.ownerKey, ws.slug, "", body, page), http.StatusBadRequest, "invalid preview")
		}
	}
}

func TestBookingPreviewScheduleDraft(t *testing.T) {
	h, database, key, owner := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	day := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, 7)
	mustExec(t, database, `INSERT INTO availability_schedules (id,user_id,name,timezone) VALUES ('preview-schedule',?,'Draft schedule','UTC')`, owner)
	mustExec(t, database, `INSERT INTO availability_rules (id,user_id,schedule_id,day_of_week,start_time,end_time) VALUES ('preview-rule',?,'preview-schedule',?,'13:00','14:00')`, owner, int(day.Weekday()))
	query := fmt.Sprintf("?from=%s&to=%s&tz=UTC", day.Format("2006-01-02"), day.Format("2006-01-02"))
	rec := previewCall(h, key, slug, query, `{"event_type":{"schedule_id":"preview-schedule"}}`, false)
	mustStatus(t, rec, http.StatusOK, "draft schedule")
	var got struct {
		Slots []struct {
			Start string `json:"start"`
		} `json:"slots"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Slots) != 2 || !strings.Contains(got.Slots[0].Start, "T13:00:00Z") {
		t.Fatalf("wrong draft schedule: %s", rec.Body)
	}
}
