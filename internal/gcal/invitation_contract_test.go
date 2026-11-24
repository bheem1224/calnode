package gcal

import (
	"context"
	"encoding/json"
	"github.com/calnode/calnode/internal/calendar"
	"github.com/calnode/calnode/internal/connstore"
	"golang.org/x/oauth2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestInvitationContractExplicitCalendarAndAccount(t *testing.T) {
	c := newTestClient(t)
	saveDestinationConnection(t, c, "host", "primary")
	if err := c.saveToken(context.Background(), "host", "primary", "second@example.test", &oauth2.Token{AccessToken: "second-account", Expiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	_, err := c.db.Exec(`INSERT INTO connection_calendars(id,user_id,provider,account_email,calendar_id,name,check_conflicts,is_destination)VALUES('choice','host','google','second@example.test','chosen-calendar','Chosen',1,0)`)
	if err != nil {
		t.Fatal(err)
	}
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		if r.Header.Get("Authorization") != "Bearer second-account" {
			t.Error("used credentials from the default account")
		}
		if !strings.HasPrefix(r.URL.Path, "/calendars/chosen-calendar/events") || r.Method != "GET" && r.URL.Query().Get("sendUpdates") != "all" {
			t.Errorf("wrong destination or invitation notification: %s", r.URL)
		}
		if r.Method == "POST" {
			var event calEventReq
			_ = json.NewDecoder(r.Body).Decode(&event)
			if event.Summary != "Client discussion" || event.Description != "Shared notes" || len(event.Attendees) != 2 || event.Attendees[0].Email != "client@example.test" || event.Attendees[1].Email != "guest@example.test" || event.Start.DateTime != "2026-12-01T09:00:00Z" || event.End.DateTime != "2026-12-01T09:45:00Z" || event.Visibility != "private" {
				t.Errorf("incomplete invitation: %+v", event)
			}
			if r.URL.Query().Get("conferenceDataVersion") != "1" || event.ConferenceData == nil || event.ConferenceData.CreateRequest.RequestID == "" {
				t.Error("Meet generation request missing")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"event-id","hangoutLink":"https://meet.google.com/abc-defg-hij"}`))
		} else if r.Method == "GET" {
			_, _ = w.Write([]byte(`{"start":{"dateTime":"2026-12-01T09:00:00Z"},"end":{"dateTime":"2026-12-01T09:45:00Z"}}`))
		} else {
			if r.Method == "PATCH" {
				w.WriteHeader(200)
			} else {
				w.WriteHeader(204)
			}
		}
	}))
	defer srv.Close()
	c.apiBase = srv.URL
	start := time.Date(2026, 12, 1, 9, 0, 0, 0, time.UTC)
	id, link, cal, err := c.CreateEvent(context.Background(), "host", calendar.CreateEventParams{Summary: "Client discussion", Description: "Shared notes", Start: start, End: start.Add(45 * time.Minute), OrganizerEmail: "client@example.test", Guests: []string{"guest@example.test"}, AddMeet: true, Private: true, DestinationCalendarID: connstore.CalendarReference("google", "chosen-calendar", "second@example.test")})
	if err != nil || id != "event-id" || link == "" || cal != connstore.CalendarReference("google", "chosen-calendar", "second@example.test") {
		t.Fatalf("create: %s %s %s %v", id, link, cal, err)
	}
	if err = c.UpdateEvent(context.Background(), "host", cal, id, start.Add(time.Hour), start.Add(105*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err = c.CancelEvent(context.Background(), "host", cal, id); err != nil {
		t.Fatal(err)
	}
	if strings.Join(methods, ",") != "POST,GET,PATCH,DELETE" {
		t.Fatal(methods)
	}
}
