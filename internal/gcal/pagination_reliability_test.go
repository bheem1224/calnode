package gcal

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/providerhttp"
)

func TestAvailabilityFallbackReleasesAccountSlot(t *testing.T) {
	c := newTestClient(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/freeBusy" {
			fmt.Fprint(w, `{"calendars":{"primary":{"errors":[{"reason":"forbidden"}]}}}`)
			return
		}
		fmt.Fprint(w, `{"kind":"calendar#events","items":[]}`)
	}))
	defer srv.Close()
	c.apiBase = srv.URL
	cfg := providerhttp.DefaultConfig()
	cfg.AccountConcurrency = 1 // Nested requests deadlock even with one caller.
	m := providerhttp.NewWithConfig(nil, cfg)
	hc := &http.Client{Transport: m.Attach(http.DefaultTransport, "app", "account")}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := c.freeBusyForConn(ctx, fbConn{hc: hc, calIDs: []string{"primary"}}, time.Now(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal("fallback retained the first request's slot", err)
	}
}

func TestProviderPaginationRejectsRepeatedGoogleToken(t *testing.T) {
	c := newTestClient(t)
	saveDestinationConnection(t, c, "user-1", "primary")
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprint(w, `{"items":[],"nextPageToken":"same"}`)
	}))
	defer srv.Close()
	c.apiBase = srv.URL
	if _, err := c.ListCalendars(context.Background(), "user-1", ""); err == nil || calls != 2 {
		t.Fatalf("pagination did not stop: calls=%d error=%v", calls, err)
	}
}
