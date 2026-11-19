package microsoft

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
	connect(t, c, "host")
	if err := c.saveToken(context.Background(), "host", "primary", "second@example.test", "work", &oauth2.Token{AccessToken: "second-account", Expiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	_, err := c.db.Exec(`INSERT INTO connection_calendars(id,user_id,provider,account_email,calendar_id,name,check_conflicts,is_destination)VALUES('choice','host','microsoft','second@example.test','chosen-calendar','Chosen',1,0)`)
	if err != nil {
		t.Fatal(err)
	}
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		if r.Header.Get("Authorization") != "Bearer second-account" {
			t.Error("used credentials from the default account")
		}
		if r.Method == "POST" {
			if r.URL.Path != "/me/calendars/chosen-calendar/events" {
				t.Errorf("wrong calendar: %s", r.URL)
			}
			var event graphEventReq
			_ = json.NewDecoder(r.Body).Decode(&event)
			if event.Subject != "Client discussion" || event.Body == nil || event.Body.Content != "Shared notes" || len(event.Attendees) != 2 || event.Attendees[0].EmailAddress.Address != "client@example.test" || event.Attendees[1].EmailAddress.Address != "guest@example.test" || event.Start.TimeZone != "UTC" || event.Start.DateTime != "2026-12-01T09:00:00" || event.End.DateTime != "2026-12-01T09:45:00" || event.Sensitivity != "private" {
				t.Errorf("incomplete invitation: %+v", event)
			}
			if !event.IsOnlineMeeting || event.OnlineMeetingProvider != "teamsForBusiness" {
				t.Error("Teams generation missing")
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"id":"event-id","onlineMeeting":{"joinUrl":"https://teams.microsoft.com/l/meetup-join/test"}}`))
		} else {
			if r.URL.Path != "/me/events/event-id" {
				t.Errorf("wrong event: %s", r.URL)
			}
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
	id, link, cal, err := c.CreateEvent(context.Background(), "host", calendar.CreateEventParams{Summary: "Client discussion", Description: "Shared notes", Start: start, End: start.Add(45 * time.Minute), OrganizerEmail: "client@example.test", Guests: []string{"guest@example.test"}, AddMeet: true, Private: true, DestinationCalendarID: connstore.CalendarReference("microsoft", "chosen-calendar", "second@example.test")})
	if err != nil || id != "event-id" || link == "" || cal != connstore.CalendarReference("microsoft", "chosen-calendar", "second@example.test") {
		t.Fatalf("create: %s %s %s %v", id, link, cal, err)
	}
	if err = c.UpdateEvent(context.Background(), "host", cal, id, start.Add(time.Hour), start.Add(105*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err = c.CancelEvent(context.Background(), "host", cal, id); err != nil {
		t.Fatal(err)
	}
	if strings.Join(methods, ",") != "POST,PATCH,DELETE" {
		t.Fatal(methods)
	}
}
