package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/abuse"
	"github.com/calnode/calnode/internal/authsecurity"
)

func abuseCall(fn http.HandlerFunc, body, slug string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/", strings.NewReader(body))
	r.SetPathValue("slug", slug)
	w := httptest.NewRecorder()
	fn(w, r)
	return w
}

func TestVerificationBudgetsAttemptsAndReplay(t *testing.T) {
	h := securitySeed(t)
	securityExec(t, h, `INSERT INTO event_types(id,user_id,slug,name,duration_minutes) VALUES('verify','member-a','verify','Verify',30)`)
	send := func(email string) *httptest.ResponseRecorder {
		return abuseCall(h.SendVerificationCode, fmt.Sprintf(`{"email":%q}`, email), "verify")
	}
	check := func(email, code string) *httptest.ResponseRecorder {
		return abuseCall(h.CheckVerificationCode, fmt.Sprintf(`{"email":%q,"code":%q}`, email, code), "verify")
	}
	if w := send("user@example.com"); w.Code != 200 {
		t.Fatalf("send: %d %s", w.Code, w.Body)
	}
	var code string
	if err := h.db.QueryRow(`SELECT code FROM email_verification_codes WHERE email='user@example.com'`).Scan(&code); err != nil {
		t.Fatal(err)
	}
	// A fresh handler (or client/IP) cannot reset the durable cooldown.
	restarted := New(h.db, h.logger)
	if w := abuseCall(restarted.SendVerificationCode, `{"email":"USER@example.com"}`, "verify"); w.Code != 429 {
		t.Fatalf("cooldown: %d %s", w.Code, w.Body)
	}
	var retained string
	_ = h.db.QueryRow(`SELECT code FROM email_verification_codes WHERE email='user@example.com'`).Scan(&retained)
	if retained != code {
		t.Fatal("throttled resend invalidated code")
	}
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	for range 5 {
		if w := check("user@example.com", wrong); w.Code != 400 {
			t.Fatal(w.Code, w.Body)
		}
	}
	if w := check("user@example.com", code); w.Code != 400 {
		t.Fatal("attempt cap bypassed", w.Code, w.Body)
	}
	if w := send("other@example.com"); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if err := h.db.QueryRow(`SELECT code FROM email_verification_codes WHERE email='other@example.com'`).Scan(&code); err != nil {
		t.Fatal(err)
	}
	var success atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if check("other@example.com", code).Code == 200 {
				success.Add(1)
			}
		}()
	}
	wg.Wait()
	if success.Load() != 1 {
		t.Fatalf("OTP replay: %d successes", success.Load())
	}
}

func TestPasswordBackoffPersistsAndExpires(t *testing.T) {
	h := securitySeed(t)
	ctx := context.Background()
	for range 5 {
		if err := h.reservePasswordAttempt(ctx, "a@example.com"); err != nil {
			t.Fatal(err)
		}
	}
	restarted := New(h.db, h.logger)
	if err := restarted.reservePasswordAttempt(ctx, "a@example.com"); err != abuse.ErrLimited {
		t.Fatalf("restart reset throttle: %v", err)
	}
	securityExec(t, h, `UPDATE login_backoff SET retry_at=0`)
	if err := h.reservePasswordAttempt(ctx, "a@example.com"); err != nil {
		t.Fatal(err)
	}
	var failures int
	var retry int64
	if err := h.db.QueryRow(`SELECT failures,retry_at FROM login_backoff WHERE account_hash=?`, abuse.Hash("a@example.com")).Scan(&failures, &retry); err != nil {
		t.Fatal(err)
	}
	if failures != 6 || retry < time.Now().Add(time.Second).UnixMilli() {
		t.Fatal("backoff did not increase", failures, retry)
	}
	if err := h.reservePasswordAttempt(ctx, "another@example.com"); err != nil {
		t.Fatal("unrelated account blocked", err)
	}
	securityExec(t, h, `UPDATE login_backoff SET expires_at=0`)
	if err := h.reservePasswordAttempt(ctx, "a@example.com"); err != nil {
		t.Fatal("expired backoff", err)
	}
}

func TestMFAAccountBudgetCannotResetWithNewChallenge(t *testing.T) {
	h := securitySeed(t)
	for range 10 {
		if h.verifyNativeCode(context.Background(), "member-a", "000000") {
			t.Fatal("invalid code accepted")
		}
	}
	// A valid backup code must remain unused once the shared attempt cap is hit.
	code := "12345678901234567890123456789012"
	securityExec(t, h, `INSERT INTO auth_recovery_codes(user_id,code_hash) VALUES('member-a',?)`, authsecurity.Hash(code))
	if h.verifyNativeCode(context.Background(), "member-a", code) {
		t.Fatal("account budget bypassed")
	}
	var n int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM auth_recovery_codes WHERE code_hash=?`, authsecurity.Hash(code)).Scan(&n); err != nil || n != 1 {
		t.Fatal("limited request consumed backup", n, err)
	}
}

func TestMCPRegistrationValidationQuotaAndCleanup(t *testing.T) {
	h := securitySeed(t)
	for _, raw := range []string{"javascript:alert(1)", "data:text/html,hello", "file:///tmp/x", "https:///missing-host", "https://user:pass@example.com/cb", "https://example.com/cb#fragment", "http://example.com/cb", "custom:opaque"} {
		if validRedirectURI(raw) {
			t.Errorf("accepted unsafe redirect %q", raw)
		}
	}
	for _, raw := range []string{"https://claude.ai/cb", "http://127.0.0.1:1234/callback", "http://[::1]:1234/callback", "com.example.agent:/oauth/callback", "vscode://extension/callback"} {
		if !validRedirectURI(raw) {
			t.Errorf("rejected %q", raw)
		}
	}
	t.Setenv("MCP_REGISTRATION_DAILY_LIMIT", "2")
	for i, want := range []int{201, 201, 429} {
		w := abuseCall(h.RegisterOAuthClient, `{"client_name":"Test","redirect_uris":["https://example.com/cb"]}`, "")
		if w.Code != want {
			t.Fatalf("registration %d: %d %s", i, w.Code, w.Body)
		}
	}
	// An old client without grants expires; one with a usable refresh grant stays.
	securityExec(t, h, `UPDATE oauth_clients SET created_at='2000-01-01T00:00:00Z'`)
	var id string
	if err := h.db.QueryRow(`SELECT client_id FROM oauth_clients LIMIT 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	securityExec(t, h, `INSERT INTO oauth_access_tokens(id,token_hash,refresh_hash,client_id,user_id,expires_at,created_at) VALUES('kept','hash','refresh',?,'member-a','2001-01-01','2000-01-01')`, id)
	tx, err := h.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = abuse.CleanupOAuthClients(context.Background(), tx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = h.db.QueryRow(`SELECT COUNT(*) FROM oauth_clients`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("cleanup: %d %v", count, err)
	}
}
