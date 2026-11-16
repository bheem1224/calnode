package handler_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCancelReasonPartyPolicy(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	// Patch to host_only.
	patchRec := httptest.NewRecorder()
	patchReq := authReq(http.MethodPatch, "/v1/event-types/"+slug, `{"require_cancel_reason":"host_only"}`, key)
	patchReq.SetPathValue("slug", slug)
	h.RequireAuth(h.PatchEventType)(patchRec, patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch host_only: %d — %s", patchRec.Code, patchRec.Body.String())
	}

	// Create booking 1.
	body1 := fmt.Sprintf(`{"event_type_slug":%q,"start_at":"2026-11-23T11:00:00Z","name":"Alice","email":"alice@example.com"}`, slug)
	req1 := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(body1))
	req1.Header.Set("Content-Type", "application/json")
	rec1 := httptest.NewRecorder()
	h.CreateBooking(rec1, req1)
	b1 := mustCreated(t, rec1, "create booking 1")
	id1 := mustString(t, b1, "id", "create booking 1")

	// Host cancel without reason fails under host_only.
	badRec := httptest.NewRecorder()
	badReq := authReq(http.MethodPost, "/v1/bookings/"+id1+"/cancel", `{}`, key)
	badReq.SetPathValue("id", id1)
	h.RequireAuth(h.CancelBooking)(badRec, badReq)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("host cancel without reason (host_only): got %d, want 400", badRec.Code)
	}

	// Host cancel with reason succeeds and preserves caller attribution.
	okRec := httptest.NewRecorder()
	okReq := authReq(http.MethodPost, "/v1/bookings/"+id1+"/cancel", `{"reason":"conflict"}`, key)
	okReq.SetPathValue("id", id1)
	h.RequireAuth(h.CancelBooking)(okRec, okReq)
	if okRec.Code != http.StatusOK {
		t.Fatalf("host cancel with reason: got %d — %s", okRec.Code, okRec.Body.String())
	}
	var cancelled struct {
		CancellationReason string `json:"cancellation_reason"`
	}
	if err := json.Unmarshal(okRec.Body.Bytes(), &cancelled); err != nil {
		t.Fatalf("decode cancelled booking: %v", err)
	}
	if !strings.HasPrefix(cancelled.CancellationReason, "Cancelled by ") || !strings.HasSuffix(cancelled.CancellationReason, ": conflict") {
		t.Errorf("cancellation_reason = %q; want 'Cancelled by ...: conflict'", cancelled.CancellationReason)
	}

	// Patch to attendee_only and verify host cancel without reason succeeds.
	patch2Rec := httptest.NewRecorder()
	patch2Req := authReq(http.MethodPatch, "/v1/event-types/"+slug, `{"require_cancel_reason":"attendee_only"}`, key)
	patch2Req.SetPathValue("slug", slug)
	h.RequireAuth(h.PatchEventType)(patch2Rec, patch2Req)
	if patch2Rec.Code != http.StatusOK {
		t.Fatalf("patch attendee_only: %d — %s", patch2Rec.Code, patch2Rec.Body.String())
	}

	body2 := fmt.Sprintf(`{"event_type_slug":%q,"start_at":"2026-11-23T12:00:00Z","name":"Bob","email":"bob@example.com"}`, slug)
	req2 := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	h.CreateBooking(rec2, req2)
	b2 := mustCreated(t, rec2, "create booking 2")
	id2 := mustString(t, b2, "id", "create booking 2")

	ok2Rec := httptest.NewRecorder()
	ok2Req := authReq(http.MethodPost, "/v1/bookings/"+id2+"/cancel", `{}`, key)
	ok2Req.SetPathValue("id", id2)
	h.RequireAuth(h.CancelBooking)(ok2Rec, ok2Req)
	if ok2Rec.Code != http.StatusOK {
		t.Fatalf("host cancel without reason (attendee_only): got %d — %s", ok2Rec.Code, ok2Rec.Body.String())
	}
}
