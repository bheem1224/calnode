package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRetiredDynamicGroupEndpoints(t *testing.T) {
	h := &Handler{}
	for _, handler := range []http.HandlerFunc{h.GetDynamicGroupSlots, h.CreateDynamicGroupBooking, h.DynamicGroupBookPage} {
		rec := httptest.NewRecorder()
		handler(rec, httptest.NewRequest(http.MethodGet, "/u/old+group", nil))
		if rec.Code != http.StatusGone {
			t.Fatalf("got %d; want 410", rec.Code)
		}
	}
}
