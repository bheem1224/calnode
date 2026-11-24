package microsoft

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestProviderPaginationRejectsForeignAndRepeatedURLs(t *testing.T) {
	var leaked atomic.Int32
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked.Add(1)
		w.WriteHeader(500)
	}))
	defer foreign.Close()
	for _, mode := range []string{"foreign", "repeated"} {
		t.Run(mode, func(t *testing.T) {
			c := newTestClient(t)
			connect(t, c, "u1")
			var srv *httptest.Server
			srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				next := foreign.URL + "/steal-token"
				if mode == "repeated" {
					next = srv.URL + r.URL.RequestURI()
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"value": []any{}, "@odata.nextLink": next})
			}))
			defer srv.Close()
			c.apiBase = srv.URL
			if _, err := c.ListCalendars(context.Background(), "u1", ""); err == nil {
				t.Fatal("accepted unsafe calendar pagination")
			}
			hc, err := c.clientForAccount(context.Background(), "u1", "")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := c.calendarView(context.Background(), hc, srv.URL+"/me/calendarView", time.Now(), time.Now().Add(time.Hour)); err == nil {
				t.Fatal("accepted unsafe availability pagination")
			}
		})
	}
	if leaked.Load() != 0 {
		t.Fatal("bearer token sent to a foreign pagination origin")
	}
}
