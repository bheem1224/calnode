package handler_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/uid"
)

func TestManageSlotsPrivateSingleUseBooking(t *testing.T) {
	h, database, key, host := setupWorkspaceWithDB(t)
	slug, eventID := seedEventTypeHTTP(t, h, key)
	start := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, 3)
	if _, err := database.Exec(`UPDATE event_types SET is_public=0, max_per_day=1, show_first_slot_only=1 WHERE id=?`, eventID); err != nil {
		t.Fatal(err)
	}
	mint := authReq("POST", "/v1/event-types/"+eventID+"/single-use-links", `{"expires_in_hours":72}`, key)
	mint.SetPathValue("id", eventID)
	minted := httptest.NewRecorder()
	h.RequireAuth(h.CreateSingleUseLink)(minted, mint)
	if minted.Code != http.StatusCreated {
		t.Fatalf("mint: %d %s", minted.Code, minted.Body.String())
	}
	var link struct{ Token string }
	if err := json.Unmarshal(minted.Body.Bytes(), &link); err != nil {
		t.Fatal(err)
	}
	create := httptest.NewRequest("POST", "/v1/t/"+link.Token+"/bookings", strings.NewReader(fmt.Sprintf(`{"start_at":%q,"name":"Test Founder","email":"founder@example.test","timezone":"UTC"}`, start.Format(time.RFC3339))))
	create.SetPathValue("token", link.Token)
	created := httptest.NewRecorder()
	h.CreateSingleUseBooking(created, create)
	if created.Code != http.StatusCreated {
		t.Fatalf("book private link: %d %s", created.Code, created.Body.String())
	}
	var booked struct{ ID string }
	if err := json.Unmarshal(created.Body.Bytes(), &booked); err != nil {
		t.Fatal(err)
	}
	token := issueTestToken(t, database, booked.ID)
	// A later default-duration change must not change this booking's duration.
	if _, err := database.Exec(`UPDATE event_types SET duration_minutes=15, allow_multiple_durations=0 WHERE id=?`, eventID); err != nil {
		t.Fatal(err)
	}
	day := start.Format("2006-01-02")
	visit := func(tok, query string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/manage/"+tok+"/slots?from="+day+"&to="+day+"&tz=UTC"+query, nil)
		r.SetPathValue("token", tok)
		w := httptest.NewRecorder()
		h.ManageSlots(w, r)
		return w
	}
	if bad := visit("invalid-token", ""); bad.Code != http.StatusNotFound {
		t.Fatalf("invalid token: %d", bad.Code)
	}
	// Public availability stays private, including when passed the consumed token.
	for _, query := range []string{"", "?single_use_token=" + link.Token} {
		r := httptest.NewRequest("GET", "/v1/event-types/"+slug+"/slots"+query, nil)
		r.SetPathValue("slug", slug)
		w := httptest.NewRecorder()
		h.GetSlots(w, r)
		if w.Code != http.StatusNotFound {
			t.Fatalf("private public slots: %d", w.Code)
		}
	}
	w := visit(token, "&duration=15&slug=another-private-event")
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("manage slots: %d %s", w.Code, w.Body.String())
	}
	var response struct {
		Slots []struct {
			Start, End string
			HostIDs    []string `json:"host_ids"`
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Slots) == 0 {
		t.Fatal("existing booking incorrectly consumed its own frequency cap")
	}
	for _, slot := range response.Slots {
		from, _ := time.Parse(time.RFC3339, slot.Start)
		to, _ := time.Parse(time.RFC3339, slot.End)
		if from.Equal(start) || to.Sub(from) != 30*time.Minute || len(slot.HostIDs) != 1 || slot.HostIDs[0] != host {
			t.Fatal("manage availability changed the booked duration or assigned hosts")
		}
	}
	page := httptest.NewRecorder()
	pageReq := httptest.NewRequest("GET", "/manage/"+token, nil)
	pageReq.SetPathValue("token", token)
	h.ManagePage(page, pageReq)
	if !strings.Contains(page.Body.String(), "/manage/${encodeURIComponent(TOKEN)}/slots?") || strings.Contains(page.Body.String(), "fetch(`/v1/event-types/") {
		t.Fatal("manage page still fetches public-only availability")
	}
	// Expiry, disabled policy and cancellation must close the read authorization.
	for _, tc := range []struct {
		name, query string
		status      int
	}{
		{"disabled", `UPDATE event_types SET disable_reschedule=1 WHERE id='` + eventID + `'`, http.StatusForbidden},
		{"re-enabled", `UPDATE event_types SET disable_reschedule=0 WHERE id='` + eventID + `'`, http.StatusOK},
	} {
		if _, err := database.Exec(tc.query); err != nil {
			t.Fatal(err)
		}
		if got := visit(token, ""); got.Code != tc.status {
			t.Fatalf("%s: %d", tc.name, got.Code)
		}
	}
	if _, err := database.Exec(`UPDATE bookings SET status='cancelled' WHERE id=?`, booked.ID); err != nil {
		t.Fatal(err)
	}
	if got := visit(token, ""); got.Code != http.StatusForbidden {
		t.Fatalf("cancelled: %d", got.Code)
	}
	if _, err := database.Exec(`UPDATE booking_manage_tokens SET expires_at=? WHERE booking_id=?`, start.AddDate(-1, 0, 0).Format(time.RFC3339), booked.ID); err != nil {
		t.Fatal(err)
	}
	if got := visit(token, ""); got.Code != http.StatusNotFound {
		t.Fatalf("expired: %d", got.Code)
	}
}

func TestManageSlotsKeepAssignedHostsAndOtherConflicts(t *testing.T) {
	h, database, key, host := setupWorkspaceWithDB(t)
	slug, eventID := seedEventTypeHTTP(t, h, key)
	start := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, 3).Add(10 * time.Hour)
	id := createBookingViaHTTP(t, h, slug, start.Format(time.RFC3339))
	// A rotation member who was not assigned must not contribute availability.
	other := uid.New()
	if _, err := database.Exec(`INSERT INTO users(id,name,email,iana_timezone) VALUES(?, 'Other', ?, 'UTC')`, other, other+"@example.test"); err != nil {
		t.Fatal(err)
	}
	seedFullAvailabilityDB(t, database, other)
	if _, err := database.Exec(`UPDATE event_types SET routing_mode='round_robin' WHERE id=?`, eventID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO event_type_hosts(event_type_id,user_id,role) VALUES(?,?,'rotation')`, eventID, other); err != nil {
		t.Fatal(err)
	}
	conflictStart := start.Add(time.Hour)
	createBookingViaHTTP(t, h, slug, conflictStart.Format(time.RFC3339))
	// Ensure the conflicting booking belongs to the original host, independent of routing.
	if _, err := database.Exec(`INSERT OR IGNORE INTO booking_hosts(booking_id,user_id) SELECT id,? FROM bookings WHERE id!=?`, host, id); err != nil {
		t.Fatal(err)
	}
	token := issueTestToken(t, database, id)
	r := httptest.NewRequest("GET", "/manage/"+token+"/slots?from="+start.Format("2006-01-02")+"&to="+start.Format("2006-01-02")+"&tz=UTC", nil)
	r.SetPathValue("token", token)
	w := httptest.NewRecorder()
	h.ManageSlots(w, r)
	if w.Code != 200 {
		t.Fatalf("slots: %d %s", w.Code, w.Body.String())
	}
	var result struct {
		Slots []struct {
			Start   string
			HostIDs []string `json:"host_ids"`
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Slots) == 0 {
		t.Fatal("no reschedule availability")
	}
	for _, s := range result.Slots {
		if len(s.HostIDs) != 1 || s.HostIDs[0] != host || s.Start == conflictStart.Format(time.RFC3339) {
			t.Fatal("offered another host or a conflicting slot")
		}
	}
}
