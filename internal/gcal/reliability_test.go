package gcal

import (
	"context"
	"encoding/json"
	"github.com/calnode/calnode/internal/calendar"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestStableEventSurvivesLostResponseConcurrentRetriesAndRestart(t *testing.T) {
	for _, meetingType := range []string{"Pitch", "Technical diligence", "Founder diligence", "Commercialization diligence"} {
		t.Run(meetingType, func(t *testing.T) {
			c := newTestClient(t)
			saveDestinationConnection(t, c, "user-1", "primary")
			var mu sync.Mutex
			var stored *calEventReq
			creates, patches, cancellations := 0, 0, 0
			unavailable := true
			failOnce := true
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				if unavailable {
					w.Header().Set("Retry-After", "0")
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				switch r.Method {
				case "GET":
					if stored == nil {
						w.WriteHeader(404)
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"id": stored.ID, "extendedProperties": stored.ExtendedProperties, "start": stored.Start, "end": stored.End, "hangoutLink": "https://meet.example.test/same"})
				case "POST":
					var body calEventReq
					_ = json.NewDecoder(r.Body).Decode(&body)
					if body.ID == "" || body.ConferenceData.CreateRequest.RequestID != body.ID {
						t.Error("missing stable event and conference ID")
					}
					if stored != nil {
						if stored.ID != body.ID {
							t.Error("changed retry ID")
						}
						w.WriteHeader(409)
						return
					}
					stored = &body
					creates++
					if failOnce {
						failOnce = false
						w.WriteHeader(503)
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]string{"id": body.ID})
				case "PATCH":
					patches++
					var body calEventReq
					_ = json.NewDecoder(r.Body).Decode(&body)
					stored.Start = body.Start
					stored.End = body.End
					w.WriteHeader(503) // applied, but its response was lost
				case "DELETE":
					if stored == nil {
						w.WriteHeader(410)
						return
					}
					stored = nil
					cancellations++
					w.WriteHeader(503) // deleted, but acknowledgement lost
				default:
					t.Errorf("unexpected %s", r.Method)
				}
			}))
			defer srv.Close()
			c.apiBase = srv.URL
			start := time.Date(2027, 1, 1, 10, 0, 0, 0, time.UTC)
			p := calendar.CreateEventParams{IdempotencyKey: "booking-host:" + meetingType, Summary: meetingType, Start: start, End: start.Add(time.Hour), AddMeet: true, OrganizerEmail: "guest@example.test"}
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			_, _, _, outageErr := c.CreateEvent(ctx, "user-1", p)
			cancel()
			if outageErr == nil {
				t.Fatal("complete provider outage reported success")
			}
			mu.Lock()
			if creates != 0 {
				t.Fatal("unavailable provider created an event or invitation")
			}
			unavailable = false
			mu.Unlock()
			var wg sync.WaitGroup
			for i := 0; i < 6; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					id, join, _, err := c.CreateEvent(context.Background(), "user-1", p)
					if err != nil || id == "" || join == "" {
						t.Errorf("recovery failed: %s %s %v", id, join, err)
					}
				}()
			}
			wg.Wait()
			restarted, err := New(c.db, "client-id", "client-secret", "http://localhost/callback", testKeyHex)
			if err != nil {
				t.Fatal(err)
			}
			restarted.apiBase = srv.URL
			id, _, _, err := restarted.CreateEvent(context.Background(), "user-1", p)
			if err != nil {
				t.Fatal(err)
			}
			// An applied PATCH is never blindly replayed after a 503. The next call reads
			// its new times and succeeds without producing another update invitation.
			moved := start.Add(2 * time.Hour)
			if err = restarted.UpdateEvent(context.Background(), "user-1", "", id, moved, moved.Add(time.Hour)); err == nil {
				t.Fatal("ambiguous PATCH hidden")
			}
			if err = restarted.UpdateEvent(context.Background(), "user-1", "", id, moved, moved.Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			var updateErrors atomic.Int32
			for i := 0; i < 6; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					next := moved.Add(time.Hour)
					if restarted.UpdateEvent(context.Background(), "user-1", "", id, next, next.Add(time.Hour)) != nil {
						updateErrors.Add(1)
					}
				}()
			}
			wg.Wait()
			if updateErrors.Load() != 1 {
				t.Fatalf("concurrent reschedule: got %d ambiguous responses, want 1", updateErrors.Load())
			}
			precise := moved.Add(time.Hour + 500*time.Millisecond)
			if err = restarted.UpdateEvent(context.Background(), "user-1", "", id, precise, precise.Add(time.Hour)); err != nil {
				t.Fatal("encoded precision repeated an update", err)
			}
			if err = restarted.CancelEvent(context.Background(), "user-1", "", id); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if creates != 1 || patches != 2 || cancellations != 1 {
				t.Fatalf("created %d events, sent %d distinct updates, %d cancellations", creates, patches, cancellations)
			}
		})
	}
}
