package caldav

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/calendar"
)

func TestStableCalDAVCreateUpdateAndDeleteSurviveLostResponses(t *testing.T) {
	c := newTestClient(t)
	seedUser(t, c.db, "host")
	var mu sync.Mutex
	stored := ""
	version, creates, updates, deletes := 0, 0, 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		user, pass, _ := r.BasicAuth()
		if user != "owner" || pass != "secret" {
			t.Error("wrong credentials")
			w.WriteHeader(401)
			return
		}
		switch r.Method {
		case "GET":
			if stored == "" {
				w.WriteHeader(404)
				return
			}
			w.Header().Set("ETag", fmt.Sprintf(`"v%d"`, version))
			io.WriteString(w, stored)
		case "PUT":
			body, _ := io.ReadAll(r.Body)
			if r.Header.Get("If-None-Match") == "*" {
				if stored != "" {
					w.WriteHeader(412)
					return
				}
				stored = string(body)
				creates++
				version++
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				_ = conn.Close()
				return
			}
			if r.Header.Get("If-Match") != fmt.Sprintf(`"v%d"`, version) {
				w.WriteHeader(412)
				return
			}
			stored = string(body)
			updates++
			version++
			w.WriteHeader(503)
		case "DELETE":
			if stored == "" {
				w.WriteHeader(404)
				return
			}
			stored = ""
			deletes++
			w.WriteHeader(503)
		default:
			t.Errorf("unexpected %s", r.Method)
		}
	}))
	defer srv.Close()
	if err := c.saveConnection(context.Background(), "host", "owner", "secret", srv.URL+"/calendar/", ""); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2027, 1, 1, 10, 0, 0, 0, time.UTC)
	p := calendar.CreateEventParams{IdempotencyKey: "booking-host:stable", Start: start, End: start.Add(time.Hour)}
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, _, err := c.CreateEvent(context.Background(), "host", p); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	restarted, err := New(c.db, testKeyHex)
	if err != nil {
		t.Fatal(err)
	}
	restarted.hc.Transport = nil
	id, _, cal, err := restarted.CreateEvent(context.Background(), "host", p)
	if err != nil {
		t.Fatal(err)
	}
	if recovered, err := restarted.RecoverEvent(context.Background(), "host", cal, p.IdempotencyKey); err != nil || recovered != id {
		t.Fatal("recovery", recovered, err)
	}
	next := start.Add(time.Hour)
	if err = restarted.UpdateEvent(context.Background(), "host", cal, id, next, next.Add(time.Hour)); err == nil {
		t.Fatal("lost update acknowledgement hidden")
	}
	if err = restarted.UpdateEvent(context.Background(), "host", cal, id, next, next.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err = restarted.UpdateEvent(context.Background(), "host", cal, id, next.Add(500*time.Millisecond), next.Add(time.Hour+500*time.Millisecond)); err != nil {
		t.Fatal("encoded precision repeated an update", err)
	}
	if err = restarted.CancelEvent(context.Background(), "host", cal, id); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if creates != 1 || updates != 1 || deletes != 1 {
		t.Fatalf("creates=%d updates=%d deletes=%d", creates, updates, deletes)
	}
}

func TestStableCalDAVRefusesUnrelatedResource(t *testing.T) {
	c := newTestClient(t)
	seedUser(t, c.db, "host")
	puts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" {
			puts++
		}
		io.WriteString(w, "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID:unrelated\r\nDTSTART:20270101T100000Z\r\nDTEND:20270101T110000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")
	}))
	defer srv.Close()
	if err := c.saveConnection(context.Background(), "host", "owner", "secret", srv.URL+"/calendar/", ""); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := c.CreateEvent(context.Background(), "host", calendar.CreateEventParams{IdempotencyKey: "stable"})
	if err == nil || !strings.Contains(err.Error(), "UID mismatch") || puts != 0 {
		t.Fatal("unrelated object overwritten", puts, err)
	}
}
