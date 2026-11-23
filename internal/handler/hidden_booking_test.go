package handler_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Hidden controls discovery, not whether somebody with the booking URL can book.
// Exercise the same unauthenticated endpoints used by hosted and embedded pages.
func TestHiddenEventDirectBooking(t *testing.T) {
	for _, team := range []bool{false, true} {
		t.Run(fmt.Sprintf("team=%t", team), func(t *testing.T) {
			h, database, key, owner := setupWorkspaceWithDB(t)
			slug, id := seedEventTypeHTTP(t, h, key)
			mustExec(t, database, `UPDATE users SET handle='hidden-host' WHERE id=?`, owner)
			if team {
				mustExec(t, database, `INSERT INTO teams(id,name,slug)VALUES('hidden-team','Hidden Team','hidden-team')`)
				mustExec(t, database, `INSERT INTO team_members(id,team_id,user_id,role)VALUES('head','hidden-team',?,'owner')`, owner)
				mustExec(t, database, `UPDATE event_types SET team_id='hidden-team',routing_mode='collective' WHERE id=?`, id)
			}
			mux := http.NewServeMux()
			mux.HandleFunc("GET /book/{slug}", h.BookPage)
			mux.HandleFunc("GET /v1/event-types/{slug}/public", h.PublicEventType)
			mux.HandleFunc("GET /v1/event-types/{slug}/questions", h.ListQuestions)
			mux.HandleFunc("GET /v1/event-types/{slug}/slots", h.GetSlots)
			mux.HandleFunc("POST /v1/bookings", h.CreateBooking)
			mux.HandleFunc("POST /v1/book/{slug}/verify-email/send", h.SendVerificationCode)
			mux.HandleFunc("GET /u/{handle}", h.PersonPage)
			mux.HandleFunc("GET /team/{slug}", h.TeamPage)
			call := func(method, path, body string, status int) *httptest.ResponseRecorder {
				t.Helper()
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
				mustStatus(t, w, status, method+" "+path)
				return w
			}
			day := time.Now().UTC().AddDate(0, 0, 3).Format("2006-01-02")
			path := "/v1/event-types/" + slug + "/slots?from=" + day + "&to=" + day + "&tz=UTC"
			var public, hidden slotsBody
			if err := json.Unmarshal(call("GET", path, "", 200).Body.Bytes(), &public); err != nil || len(public.Slots) == 0 {
				t.Fatalf("public fixture must have availability: %v", err)
			}
			patch := authReq("PATCH", "/v1/event-types/"+slug, `{"is_public":false}`, key)
			patch.SetPathValue("slug", slug)
			updated := httptest.NewRecorder()
			h.RequireAuth(h.PatchEventType)(updated, patch)
			mustStatus(t, updated, 200, "hide event")
			for _, suffix := range []string{"/public", "/questions"} {
				call("GET", "/v1/event-types/"+slug+suffix, "", 200)
			}
			call("GET", "/book/"+slug, "", 200)
			if err := json.Unmarshal(call("GET", path, "", 200).Body.Bytes(), &hidden); err != nil || len(hidden.Slots) != len(public.Slots) {
				t.Fatalf("hiding changed availability: public=%d hidden=%d error=%v", len(public.Slots), len(hidden.Slots), err)
			}
			for i := range public.Slots {
				if public.Slots[i].Start != hidden.Slots[i].Start {
					t.Fatal("visibility changed an available start")
				}
			}
			for _, directory := range []string{"/u/hidden-host", "/team/hidden-team"} {
				if !team && strings.Contains(directory, "/team/") {
					continue
				}
				if strings.Contains(call("GET", directory, "", 200).Body.String(), "/book/"+slug) {
					t.Fatal("hidden event appeared in a public directory")
				}
			}
			body := fmt.Sprintf(`{"event_type_slug":%q,"start_at":%q,"name":"Direct guest","email":"direct@example.test","timezone":"UTC"}`, slug, hidden.Slots[0].Start)
			call("POST", "/v1/book/"+slug+"/verify-email/send", `{"email":"direct@example.test"}`, 200)
			created := call("POST", "/v1/bookings", body, http.StatusCreated)
			var booking struct{ ID string }
			if err := json.Unmarshal(created.Body.Bytes(), &booking); err != nil || booking.ID == "" {
				t.Fatalf("hidden booking missing ID: %v", err)
			}
			var count int
			if err := database.QueryRow(`SELECT COUNT(*) FROM bookings WHERE id=? AND event_type_id=?`, booking.ID, id).Scan(&count); err != nil || count != 1 {
				t.Fatalf("hidden booking not persisted: count=%d error=%v", count, err)
			}
		})
	}
}

func TestUnavailableEventDirectLinksStayClosed(t *testing.T) {
	for _, state := range []string{"inactive", "archived", "dynamic"} {
		t.Run(state, func(t *testing.T) {
			h, database, key, _ := setupWorkspaceWithDB(t)
			slug, id := seedEventTypeHTTP(t, h, key)
			switch state {
			case "inactive":
				mustExec(t, database, `UPDATE event_types SET is_active=0 WHERE id=?`, id)
			case "archived":
				mustExec(t, database, `UPDATE event_types SET archived_at=CURRENT_TIMESTAMP WHERE id=?`, id)
			case "dynamic":
				slug = "__dynamic__" + id
				mustExec(t, database, `UPDATE event_types SET slug=?,is_public=0 WHERE id=?`, slug, id)
			}
			for name, serve := range map[string]http.HandlerFunc{"page": h.BookPage, "metadata": h.PublicEventType, "questions": h.ListQuestions, "slots": h.GetSlots, "verification": h.SendVerificationCode} {
				r := httptest.NewRequest("GET", "/test", strings.NewReader(`{"email":"guest@example.test"}`))
				r.SetPathValue("slug", slug)
				w := httptest.NewRecorder()
				serve(w, r)
				mustStatus(t, w, 404, state+" "+name)
			}
		})
	}
}
