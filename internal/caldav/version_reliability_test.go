package caldav

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/calendar"
)

func TestStableCalDAVVersionRejectsStaleReadAfterLostResponse(t *testing.T) {
	c := newTestClient(t)
	seedUser(t, c.db, "host")
	start := time.Date(2027, 1, 1, 10, 0, 0, 0, time.UTC)
	next := start.Add(time.Hour)
	reads, writes, accepted := 0, 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			reads++
			at, version := start, `"v1"`
			if reads > 2 {
				at, version = next, `"v2"`
			}
			w.Header().Set("ETag", version)
			io.WriteString(w, buildICS("stable", at, at.Add(time.Hour), "Meeting", "", "", "", "", 0, false))
			return
		}
		if r.Method != "PUT" {
			t.Error(r.Method)
			return
		}
		writes++
		if r.Header.Get("If-Match") != `"v1"` {
			t.Error("missing version precondition")
		}
		if accepted > 0 {
			w.WriteHeader(412)
			return
		}
		accepted++
		w.WriteHeader(503)
	}))
	defer srv.Close()
	if err := c.saveConnection(context.Background(), "host", "owner", "secret", srv.URL+"/calendar/", ""); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		err := c.UpdateEvent(context.Background(), "host", srv.URL+"/calendar/", srv.URL+"/calendar/stable.ics", next, next.Add(time.Hour))
		if (i < 2) != (err != nil) {
			t.Fatal(i, err)
		}
	}
	if reads != 3 || writes != 2 || accepted != 1 {
		t.Fatal(reads, writes, accepted)
	}
}

var _ calendar.EventRecoverer = (*Client)(nil)
