package microsoft

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestStableEventUpdateRejectsStaleReadAfterLostResponse(t *testing.T) {
	c := newTestClient(t)
	connect(t, c, "u1")
	start := time.Date(2027, 1, 1, 10, 0, 0, 0, time.UTC)
	next := start.Add(time.Hour)
	var mu sync.Mutex
	reads, accepted, attempts := 0, 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case "GET":
			reads++
			at, version := start, `W/"v1"`
			if reads > 2 {
				at, version = next, `W/"v2"`
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"@odata.etag": version,
				"start":       map[string]string{"dateTime": at.Format(graphTZ), "timeZone": "UTC"},
				"end":         map[string]string{"dateTime": at.Add(time.Hour).Format(graphTZ), "timeZone": "UTC"},
			})
		case "PATCH":
			attempts++
			if r.Header.Get("If-Match") != `W/"v1"` {
				t.Error("missing version precondition")
			}
			if accepted != 0 {
				w.WriteHeader(412)
				return
			}
			accepted++
			w.WriteHeader(503)
		default:
			t.Errorf("unexpected request %s", r.Method)
		}
	}))
	defer srv.Close()
	c.apiBase = srv.URL
	for i := 0; i < 3; i++ {
		err := c.UpdateEvent(context.Background(), "u1", "", "event-one", next, next.Add(time.Hour))
		if (i < 2) != (err != nil) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if accepted != 1 || attempts != 2 || reads != 3 {
		t.Fatalf("reads=%d writes=%d accepted=%d", reads, attempts, accepted)
	}
}
