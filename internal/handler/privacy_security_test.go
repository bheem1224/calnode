package handler_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func decodeJSON200(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode JSON: %v — %s", err, rec.Body.String())
	}
	return m
}

func TestPrivacyAndSecurity_confirmAndEmailVerify(t *testing.T) {
	h, db, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	// 1. Enable requires_confirmation (free_email), unconfirmed_blocks_slots=false, require_email_verification=true.
	patchRec := httptest.NewRecorder()
	patchReq := authReq(http.MethodPatch, "/v1/event-types/"+slug, `{
		"requires_confirmation": true,
		"confirm_mode": "free_email",
		"unconfirmed_blocks_slots": false,
		"require_email_verification": true,
		"hide_notes_in_calendar": true,
		"hide_calendar_event_details": true
	}`, key)
	patchReq.SetPathValue("slug", slug)
	h.RequireAuth(h.PatchEventType)(patchRec, patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch privacy settings: %d — %s", patchRec.Code, patchRec.Body.String())
	}

	// 2. Booking without verification_token is rejected.
	noVerBody := fmt.Sprintf(`{
		"event_type_slug": %q,
		"start_at": "2026-06-20T09:00:00Z",
		"name": "Alice",
		"email": "alice@gmail.com"
	}`, slug)
	noVerRec := httptest.NewRecorder()
	noVerReq := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(noVerBody))
	noVerReq.Header.Set("Content-Type", "application/json")
	h.CreateBooking(noVerRec, noVerReq)
	if noVerRec.Code != http.StatusBadRequest {
		t.Fatalf("booking without verification token: got %d, want 400", noVerRec.Code)
	}

	// 3. Send & verify email OTP.
	sendRec := httptest.NewRecorder()
	sendReq := httptest.NewRequest(http.MethodPost, "/v1/book/"+slug+"/verify-email/send", strings.NewReader(`{"email":"alice@gmail.com"}`))
	sendReq.SetPathValue("slug", slug)
	sendReq.Header.Set("Content-Type", "application/json")
	h.SendVerificationCode(sendRec, sendReq)
	if sendRec.Code != http.StatusOK {
		t.Fatalf("send verification code: %d — %s", sendRec.Code, sendRec.Body.String())
	}
	var code string
	if err := db.QueryRow(`SELECT code FROM email_verification_codes WHERE event_type_slug = ? AND email = 'alice@gmail.com'`, slug).Scan(&code); err != nil {
		t.Fatalf("read otp code: %v", err)
	}

	checkRec := httptest.NewRecorder()
	checkReq := httptest.NewRequest(http.MethodPost, "/v1/book/"+slug+"/verify-email/check", strings.NewReader(fmt.Sprintf(`{"email":"alice@gmail.com","code":%q}`, code)))
	checkReq.SetPathValue("slug", slug)
	checkReq.Header.Set("Content-Type", "application/json")
	h.CheckVerificationCode(checkRec, checkReq)
	if checkRec.Code != http.StatusOK {
		t.Fatalf("check verification code: %d — %s", checkRec.Code, checkRec.Body.String())
	}
	vToken := mustString(t, decodeJSON200(t, checkRec), "verification_token", "check verification")

	// 4. Create booking with free email -> status = pending.
	pendBody := fmt.Sprintf(`{
		"event_type_slug": %q,
		"start_at": "2026-06-20T09:00:00Z",
		"name": "Alice",
		"email": "alice@gmail.com",
		"verification_token": %q
	}`, slug, vToken)
	pendRec := httptest.NewRecorder()
	pendReq := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(pendBody))
	pendReq.Header.Set("Content-Type", "application/json")
	h.CreateBooking(pendRec, pendReq)
	if pendRec.Code != http.StatusCreated {
		t.Fatalf("create pending booking: %d — %s", pendRec.Code, pendRec.Body.String())
	}
	pendResp := mustCreated(t, pendRec, "pending booking")
	if got := mustString(t, pendResp, "status", "pending booking"); got != "pending" {
		t.Errorf("status = %q; want pending", got)
	}
	bookingID := mustString(t, pendResp, "id", "pending booking")

	// 5. Because unconfirmed_blocks_slots=false, a corporate email can still book the same slot and auto-confirm.
	send2Rec := httptest.NewRecorder()
	send2Req := httptest.NewRequest(http.MethodPost, "/v1/book/"+slug+"/verify-email/send", strings.NewReader(`{"email":"bob@acme.io"}`))
	send2Req.SetPathValue("slug", slug)
	send2Req.Header.Set("Content-Type", "application/json")
	h.SendVerificationCode(send2Rec, send2Req)
	var code2 string
	if err := db.QueryRow(`SELECT code FROM email_verification_codes WHERE event_type_slug = ? AND email = 'bob@acme.io'`, slug).Scan(&code2); err != nil {
		t.Fatalf("read otp code 2: %v", err)
	}
	check2Rec := httptest.NewRecorder()
	check2Req := httptest.NewRequest(http.MethodPost, "/v1/book/"+slug+"/verify-email/check", strings.NewReader(fmt.Sprintf(`{"email":"bob@acme.io","code":%q}`, code2)))
	check2Req.SetPathValue("slug", slug)
	check2Req.Header.Set("Content-Type", "application/json")
	h.CheckVerificationCode(check2Rec, check2Req)
	vToken2 := mustString(t, decodeJSON200(t, check2Rec), "verification_token", "check verification 2")

	corpBody := fmt.Sprintf(`{
		"event_type_slug": %q,
		"start_at": "2026-06-20T09:00:00Z",
		"name": "Bob",
		"email": "bob@acme.io",
		"verification_token": %q
	}`, slug, vToken2)
	corpRec := httptest.NewRecorder()
	corpReq := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(corpBody))
	corpReq.Header.Set("Content-Type", "application/json")
	h.CreateBooking(corpRec, corpReq)
	if corpRec.Code != http.StatusCreated {
		t.Fatalf("create corporate booking on unblocked slot: %d — %s", corpRec.Code, corpRec.Body.String())
	}
	if got := mustString(t, mustCreated(t, corpRec, "corp booking"), "status", "corp booking"); got != "confirmed" {
		t.Errorf("corporate booking status = %q; want confirmed", got)
	}

	// 6. Create another pending booking at 10:00 and host confirms it -> confirmed.
	send3Rec := httptest.NewRecorder()
	send3Req := httptest.NewRequest(http.MethodPost, "/v1/book/"+slug+"/verify-email/send", strings.NewReader(`{"email":"carol@gmail.com"}`))
	send3Req.SetPathValue("slug", slug)
	send3Req.Header.Set("Content-Type", "application/json")
	h.SendVerificationCode(send3Rec, send3Req)
	var code3 string
	if err := db.QueryRow(`SELECT code FROM email_verification_codes WHERE event_type_slug = ? AND email = 'carol@gmail.com'`, slug).Scan(&code3); err != nil {
		t.Fatalf("read otp code 3: %v", err)
	}
	check3Rec := httptest.NewRecorder()
	check3Req := httptest.NewRequest(http.MethodPost, "/v1/book/"+slug+"/verify-email/check", strings.NewReader(fmt.Sprintf(`{"email":"carol@gmail.com","code":%q}`, code3)))
	check3Req.SetPathValue("slug", slug)
	check3Req.Header.Set("Content-Type", "application/json")
	h.CheckVerificationCode(check3Rec, check3Req)
	vToken3 := mustString(t, decodeJSON200(t, check3Rec), "verification_token", "check verification 3")

	pend2Body := fmt.Sprintf(`{
		"event_type_slug": %q,
		"start_at": "2026-06-20T10:00:00Z",
		"name": "Carol",
		"email": "carol@gmail.com",
		"verification_token": %q
	}`, slug, vToken3)
	pend2Rec := httptest.NewRecorder()
	pend2Req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(pend2Body))
	pend2Req.Header.Set("Content-Type", "application/json")
	h.CreateBooking(pend2Rec, pend2Req)
	if pend2Rec.Code != http.StatusCreated {
		t.Fatalf("create pending booking 2: %d — %s", pend2Rec.Code, pend2Rec.Body.String())
	}
	bookingID2 := mustString(t, mustCreated(t, pend2Rec, "pending booking 2"), "id", "pending booking 2")
	_ = bookingID

	confRec := httptest.NewRecorder()
	confReq := authReq(http.MethodPost, "/v1/bookings/"+bookingID2+"/confirm", `{}`, key)
	confReq.SetPathValue("id", bookingID2)
	h.RequireAuth(h.ConfirmBooking)(confRec, confReq)
	if confRec.Code != http.StatusOK {
		t.Fatalf("confirm booking: %d — %s", confRec.Code, confRec.Body.String())
	}
	if got := mustString(t, decodeJSON200(t, confRec), "status", "confirm booking"); got != "confirmed" {
		t.Errorf("confirmed status = %q; want confirmed", got)
	}
}
