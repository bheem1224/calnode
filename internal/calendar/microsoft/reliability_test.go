package microsoft

import (
	"context"
	"encoding/json"
	"github.com/calnode/calnode/internal/calendar"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestStableEventSurvivesLostResponseConcurrentRetriesAndRestart(t *testing.T) {
	c := newTestClient(t)
	connect(t, c, "u1")
	var mu sync.Mutex
	var stored *graphEventReq
	creates, patches, cancellations := 0, 0, 0
	failed := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		event := func() map[string]any {
			return map[string]any{"id": "event-one", "start": stored.Start, "end": stored.End, "onlineMeeting": map[string]string{"joinUrl": "https://teams.example.test/same"}}
		}
		switch r.Method {
		case "GET":
			if strings.HasSuffix(r.URL.Path, "/events") {
				items := []map[string]any{}
				if stored != nil {
					if !strings.Contains(r.URL.Query().Get("$filter"), stored.TransactionID) {
						t.Error("missing exact operation lookup")
					}
					items = append(items, event())
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"value": items})
			} else {
				if stored == nil {
					w.WriteHeader(404)
					return
				}
				_ = json.NewEncoder(w).Encode(event())
			}
		case "POST":
			var body graphEventReq
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.TransactionID == "" || len(body.Properties) != 1 {
				t.Error("operation identity missing")
			}
			if stored == nil {
				stored = &body
				creates++
			} else if stored.TransactionID != body.TransactionID {
				t.Error("transaction changed")
			}
			if !failed {
				failed = true
				w.WriteHeader(503)
				return
			}
			w.WriteHeader(201)
			_ = json.NewEncoder(w).Encode(event())
		case "PATCH":
			patches++
			var body graphEventReq
			_ = json.NewDecoder(r.Body).Decode(&body)
			stored.Start = body.Start
			stored.End = body.End
			w.WriteHeader(503)
		case "DELETE":
			if stored == nil {
				w.WriteHeader(404)
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
	p := calendar.CreateEventParams{IdempotencyKey: "booking-host:one", Start: start, End: start.Add(time.Hour), AddMeet: true, OrganizerEmail: "guest@example.test"}
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, join, _, err := c.CreateEvent(context.Background(), "u1", p)
			if err != nil || id == "" || join == "" {
				t.Errorf("recovery: %s %s %v", id, join, err)
			}
		}()
	}
	wg.Wait()
	restarted, err := New(c.db, "client-id", "client-secret", "common", "http://localhost/cb", testKeyHex)
	if err != nil {
		t.Fatal(err)
	}
	restarted.apiBase = srv.URL
	id, _, _, err := restarted.CreateEvent(context.Background(), "u1", p)
	if err != nil {
		t.Fatal(err)
	}
	moved := start.Add(2 * time.Hour)
	if err = restarted.UpdateEvent(context.Background(), "u1", "", id, moved, moved.Add(time.Hour)); err == nil {
		t.Fatal("ambiguous PATCH hidden")
	}
	if err = restarted.UpdateEvent(context.Background(), "u1", "", id, moved, moved.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var updateErrors atomic.Int32
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			next := moved.Add(time.Hour)
			if restarted.UpdateEvent(context.Background(), "u1", "", id, next, next.Add(time.Hour)) != nil {
				updateErrors.Add(1)
			}
		}()
	}
	wg.Wait()
	if updateErrors.Load() != 1 {
		t.Fatalf("concurrent reschedule: got %d ambiguous responses, want 1", updateErrors.Load())
	}
	precise := moved.Add(time.Hour + 500*time.Millisecond)
	if err = restarted.UpdateEvent(context.Background(), "u1", "", id, precise, precise.Add(time.Hour)); err != nil {
		t.Fatal("encoded precision repeated an update", err)
	}
	if err = restarted.CancelEvent(context.Background(), "u1", "", id); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if creates != 1 || patches != 2 || cancellations != 1 {
		t.Fatalf("created %d events, sent %d distinct updates, %d cancellations", creates, patches, cancellations)
	}
}
