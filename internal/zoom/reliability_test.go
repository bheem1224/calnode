package zoom

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/secret"
	"golang.org/x/oauth2"
)

func TestZoomUncertainCreateRecoversAcrossRestartWithoutAnotherPOST(t *testing.T) {
	c := newTestClient(t)
	seedUser(t, c.db, "host")
	connectZoom(t, c.db, "host", "access")
	var mu sync.Mutex
	var stored map[string]any
	posts, accepted, deletes, updates := 0, 0, 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case "POST":
			posts++
			if stored != nil {
				t.Error("another uncertain create was sent")
			}
			_ = json.NewDecoder(r.Body).Decode(&stored)
			stored["id"] = 12345
			stored["join_url"] = "https://zoom.example.test/j/12345"
			accepted++
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
		case "GET":
			if r.URL.Path == "/users/me/meetings" {
				items := []any{}
				if stored != nil {
					items = append(items, stored)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"meetings": items})
				return
			}
			if stored == nil {
				w.WriteHeader(404)
				return
			}
			_ = json.NewEncoder(w).Encode(stored)
		case "PATCH":
			var change map[string]any
			_ = json.NewDecoder(r.Body).Decode(&change)
			stored["start_time"] = change["start_time"]
			stored["duration"] = change["duration"]
			updates++
			w.WriteHeader(503)
		case "DELETE":
			if stored == nil {
				w.WriteHeader(404)
				return
			}
			stored = nil
			deletes++
			w.WriteHeader(503)
		}
	}))
	defer srv.Close()
	c.apiBase = srv.URL
	start := time.Date(2027, 1, 1, 10, 0, 0, 0, time.UTC)
	p := MeetingParams{OperationKey: "booking:one", Topic: "Meeting", Start: start, DurationMinutes: 30, Timezone: "UTC"}
	if _, _, err := c.CreateMeeting(context.Background(), "host", p); err == nil {
		t.Fatal("uncertain create hidden")
	}
	restarted, err := New(c.db, "client-id", "client-secret", "http://localhost/callback", testKeyHex)
	if err != nil {
		t.Fatal(err)
	}
	restarted.apiBase = srv.URL
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			join, id, err := restarted.CreateMeeting(context.Background(), "host", p)
			if err != nil || id != "12345" || join == "" {
				t.Error("recovery", join, id, err)
			}
		}()
	}
	wg.Wait()
	if err = restarted.UpdateMeeting(context.Background(), "host", "12345", start.Add(time.Hour), 30, ""); err == nil {
		t.Fatal("lost PATCH hidden")
	}
	if err = restarted.UpdateMeeting(context.Background(), "host", "12345", start.Add(time.Hour), 30, ""); err != nil {
		t.Fatal(err)
	}
	if err = restarted.UpdateMeeting(context.Background(), "host", "12345", start.Add(time.Hour+500*time.Millisecond), 30, ""); err != nil {
		t.Fatal("encoded precision repeated an update", err)
	}
	if _, err = c.db.Exec(`UPDATE zoom_meeting_intents SET cleanup_requested=1,next_attempt_at=''`); err != nil {
		t.Fatal(err)
	}
	if err = restarted.ReconcileCleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if posts != 1 || accepted != 1 || updates != 1 || deletes != 1 {
		t.Fatal("duplicate side effect", posts, accepted, updates, deletes)
	}
}

func TestZoomUnresolvedOutcomeNeverBlindlyCreatesAgain(t *testing.T) {
	c := newTestClient(t)
	seedUser(t, c.db, "host")
	connectZoom(t, c.db, "host", "access")
	var posts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posts.Add(1)
			w.WriteHeader(503)
			return
		}
		_, _ = w.Write([]byte(`{"meetings":[]}`))
	}))
	defer srv.Close()
	c.apiBase = srv.URL
	p := MeetingParams{OperationKey: "booking:unknown", Topic: "Meeting", Start: time.Now().Add(time.Hour), DurationMinutes: 30}
	_, _, _ = c.CreateMeeting(context.Background(), "host", p)
	for i := 0; i < 5; i++ {
		if _, _, err := c.CreateMeeting(context.Background(), "host", p); !errors.Is(err, ErrUncertainCreation) {
			t.Fatal(err)
		}
	}
	if posts.Load() != 1 {
		t.Fatal("unknown outcome replayed", posts.Load())
	}
}

func TestZoomRefreshIsCoordinatedAndRotatedTokenPersists(t *testing.T) {
	c := newTestClient(t)
	seedUser(t, c.db, "host")
	connectZoom(t, c.db, "host", "old")
	refresh, _ := secret.Encrypt(c.key, "original-refresh")
	if _, err := c.db.Exec(`UPDATE zoom_connections SET expiry_at='2000-01-01T00:00:00Z',refresh_token_enc=?`, refresh); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = r.ParseForm()
		if r.Form.Get("refresh_token") != "original-refresh" {
			t.Error("stale refresh token")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"fresh","refresh_token":"rotated","expires_in":3600,"token_type":"Bearer"}`))
	}))
	defer srv.Close()
	c.config.Endpoint.TokenURL = srv.URL
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			hc, err := c.authedClient(context.Background(), "host")
			if err != nil {
				t.Error(err)
				return
			}
			tok, err := hc.Transport.(*oauth2.Transport).Source.Token()
			if err != nil || tok.AccessToken != "fresh" {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal("refresh storm", calls.Load())
	}
	var encrypted string
	if err := c.db.QueryRow(`SELECT refresh_token_enc FROM zoom_connections WHERE user_id='host'`).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if value, err := secret.Decrypt(c.key, encrypted); err != nil || value != "rotated" {
		t.Fatal("rotation lost", err)
	}
}

func TestZoomCancellationRecoversIDBeforeCreateWasAcknowledged(t *testing.T) {
	c := newTestClient(t)
	seedUser(t, c.db, "host")
	connectZoom(t, c.db, "host", "access")
	_, err := c.db.Exec(`INSERT INTO event_types(id,user_id,slug,name,duration_minutes)VALUES('et','host','meeting','Meeting',30);INSERT INTO bookings(id,event_type_id,host_id,start_at,end_at,status)VALUES('bk','et','host','2099-01-01T10:00:00Z','2099-01-01T10:30:00Z','confirmed')`)
	if err != nil {
		t.Fatal(err)
	}
	var marker string
	posts, deletes := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "POST":
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			marker = in["agenda"].(string)
			posts++
			w.WriteHeader(503)
		case "GET":
			_ = json.NewEncoder(w).Encode(map[string]any{"meetings": []any{map[string]any{"id": 12345, "agenda": marker, "join_url": "https://zoom.example.test/j/12345"}}})
		case "DELETE":
			deletes++
			w.WriteHeader(204)
		}
	}))
	defer srv.Close()
	c.apiBase = srv.URL
	p := MeetingParams{BookingID: "bk", OperationKey: "booking:bk", Topic: "Meeting", Start: time.Date(2099, 1, 1, 10, 0, 0, 0, time.UTC), DurationMinutes: 30}
	if _, _, err = c.CreateMeeting(context.Background(), "host", p); err == nil {
		t.Fatal("lost acknowledgement hidden")
	}
	if _, err = c.db.Exec(`UPDATE bookings SET status='cancelled' WHERE id='bk';DELETE FROM bookings WHERE id='bk'`); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(c.db, "client-id", "client-secret", "http://localhost/callback", testKeyHex)
	if err != nil {
		t.Fatal(err)
	}
	restarted.apiBase = srv.URL
	if err = restarted.ReconcileCleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, _, err = restarted.CreateMeeting(context.Background(), "host", p); !errors.Is(err, ErrRetiredMeeting) {
		t.Fatal("cancelled operation recreated", err)
	}
	if posts != 1 || deletes != 1 {
		t.Fatal("unknown meeting not cleaned once", posts, deletes)
	}
}
