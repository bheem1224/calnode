package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/authsecurity"
	"github.com/calnode/calnode/internal/secret"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
)

const testTOTPSecret = "JBSWY3DPEHPK3PXP"

func securityTOTP(t *testing.T, h *Handler, id string) {
	t.Helper()
	encrypted, err := secret.Encrypt(h.encKey, testTOTPSecret)
	if err != nil {
		t.Fatal(err)
	}
	securityExec(t, h, `INSERT INTO user_totp(user_id,secret_enc,confirmed)VALUES(?,?,1)`, id, encrypted)
}
func securitySession(t *testing.T, h *Handler, id string, verified, setup bool) *http.Cookie {
	t.Helper()
	raw, err := randomAuthToken()
	if err != nil {
		t.Fatal(err)
	}
	mfa := ""
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if verified {
		mfa = now
	}
	securityExec(t, h, `INSERT INTO sessions(id,user_id,expires_at,auth_method,mfa_verified_at,reauthenticated_at,recovery_only)VALUES(?,?,?,'password',?,?,?)`, raw, id, time.Now().Add(time.Hour).UTC().Format(time.RFC3339), mfa, now, setup)
	return &http.Cookie{Name: sessionCookieName, Value: raw}
}
func securityCall(h *Handler, fn http.HandlerFunc, cookie *http.Cookie, method, path, body string, values map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if cookie != nil {
		req.AddCookie(cookie)
	}
	for k, v := range values {
		req.SetPathValue(k, v)
	}
	rec := httptest.NewRecorder()
	h.RequireAuth(fn)(rec, req)
	return rec
}
func securityCookie(t *testing.T, rec *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == name && c.Value != "" {
			return c
		}
	}
	t.Fatalf("missing %s cookie: %d %s", name, rec.Code, rec.Body)
	return nil
}
func TestNativeMFA_PasswordGateReplayAndBackupCodes(t *testing.T) {
	h := securitySeed(t)
	hash, err := bcrypt.GenerateFromPassword([]byte("Strong-password-123"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	securityExec(t, h, `UPDATE users SET password_hash=?,email_login=1 WHERE id='member-a'`, string(hash))
	securityTOTP(t, h, "member-a")
	login := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.LoginEmail(rec, httptest.NewRequest("POST", "/", strings.NewReader(`{"email":"a@example.com","password":"Strong-password-123"}`)))
		return rec
	}
	rec := login()
	if rec.Code != 200 || rec.Header().Get("X-Calnode-Auth-Step") != "mfa" {
		t.Fatalf("password bypassed MFA: %d %s", rec.Code, rec.Body)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName {
			t.Fatal("password issued a full session before MFA")
		}
	}
	pending := securityCookie(t, rec, pendingAuthCookie)
	code, err := totp.GenerateCode(testTOTPSecret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	verify := func(cookie *http.Cookie, code string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/", strings.NewReader(`{"code":"`+code+`"}`))
		req.AddCookie(cookie)
		out := httptest.NewRecorder()
		h.VerifyNativeMFA(out, req)
		return out
	}
	rec = verify(pending, code)
	if rec.Code != 200 {
		t.Fatalf("valid TOTP: %d %s", rec.Code, rec.Body)
	}
	session := securityCookie(t, rec, sessionCookieName)
	if got := verify(pending, code); got.Code != 401 {
		t.Fatal("login challenge was reusable")
	}
	pending = securityCookie(t, login(), pendingAuthCookie)
	if got := verify(pending, code); got.Code != 401 {
		t.Fatal("TOTP time-step was reusable")
	}
	rec = securityCall(h, h.GenerateRecoveryCodes, session, "POST", "/", "{}", nil)
	if rec.Code != 200 {
		t.Fatalf("generate codes: %d %s", rec.Code, rec.Body)
	}
	var result struct {
		Codes []string `json:"codes"`
	}
	if err = json.Unmarshal(rec.Body.Bytes(), &result); err != nil || len(result.Codes) != 10 {
		t.Fatalf("backup codes: %v %s", err, rec.Body)
	}
	var stored string
	if err = h.db.QueryRow(`SELECT code_hash FROM auth_recovery_codes LIMIT 1`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, "-") {
		t.Fatal("plaintext recovery code stored")
	}
	rec = verify(pending, result.Codes[0])
	if rec.Code != 200 {
		t.Fatalf("backup code sign-in: %d %s", rec.Code, rec.Body)
	}
	if h.verifyNativeCode(context.Background(), "member-a", result.Codes[0]) {
		t.Fatal("backup code was reusable")
	}
	pending = securityCookie(t, login(), pendingAuthCookie)
	for i := 0; i < 5; i++ {
		if got := verify(pending, "bad-code"); got.Code != 401 {
			t.Fatal("invalid code accepted")
		}
	}
	if got := verify(pending, result.Codes[1]); got.Code != 401 {
		t.Fatal("exhausted challenge accepted a valid code")
	}
}
func TestNativeMFA_RestrictedEnrollmentCannotAccessApplication(t *testing.T) {
	h := securitySeed(t)
	securityExec(t, h, `UPDATE server_settings SET signin_policy_json='{"require_member_mfa":true,"admin_password_enabled":true}'`)
	rec := httptest.NewRecorder()
	if err := h.createSession(context.Background(), rec, "member-a"); err != nil {
		t.Fatal(err)
	}
	cookie := securityCookie(t, rec, sessionCookieName)
	if rec.Header().Get("X-Calnode-Auth-Step") != "enroll" {
		t.Fatal("enrollment step missing")
	}
	if got := securityCall(h, h.GetMe, cookie, "GET", "/", "", nil); got.Code != 403 {
		t.Fatalf("setup session accessed profile: %d %s", got.Code, got.Body)
	}
	if got := securityCall(h, h.GetAuthSecurity, cookie, "GET", "/", "", nil); got.Code != 200 {
		t.Fatalf("setup session cannot enroll: %d %s", got.Code, got.Body)
	}
	rec = securityCall(h, h.BeginTOTP, cookie, "POST", "/", "{}", nil)
	if rec.Code != 200 {
		t.Fatalf("begin: %d %s", rec.Code, rec.Body)
	}
	var setup struct {
		Token  string `json:"setup_token"`
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &setup); err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(setup.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rec = securityCall(h, h.ConfirmTOTP, cookie, "POST", "/", `{"setup_token":"`+setup.Token+`","code":"`+code+`"}`, nil)
	if rec.Code != 200 {
		t.Fatalf("confirm: %d %s", rec.Code, rec.Body)
	}
	full := securityCookie(t, rec, sessionCookieName)
	if got := securityCall(h, h.GetMe, full, "GET", "/", "", nil); got.Code != 200 {
		t.Fatalf("enrolled session: %d %s", got.Code, got.Body)
	}
	if got := securityCall(h, h.GetAuthSecurity, cookie, "GET", "/", "", nil); got.Code != 401 {
		t.Fatal("old setup session retained access")
	}
	if got := securityCall(h, h.DeleteAuthFactor, full, "DELETE", "/", "", map[string]string{"id": "totp"}); got.Code != 400 {
		t.Fatal("last required factor removable")
	}
}
func TestNativeMFA_RecoveryBoundariesAndOwnerEmergency(t *testing.T) {
	h := securitySeed(t)
	securityExec(t, h, `INSERT INTO users(id,email,name,is_admin,is_owner)VALUES('owner','o@example.com','Owner',1,1),('admin','admin@example.com','Admin',1,0)`)
	securityTOTP(t, h, "admin")
	admin := securitySession(t, h, "admin", true, false)
	for _, target := range []string{"owner", "admin"} {
		rec := securityCall(h, h.ResetUserMFA, admin, "POST", "/", `{"reason":"IT-123 verified identity"}`, map[string]string{"id": target})
		if rec.Code != 403 {
			t.Fatalf("protected target %s: %d %s", target, rec.Code, rec.Body)
		}
	}
	securityTOTP(t, h, "member-a")
	old := securitySession(t, h, "member-a", true, false)
	rec := securityCall(h, h.ResetUserMFA, admin, "POST", "/", `{"reason":"IT-123 verified identity"}`, map[string]string{"id": "member-a"})
	if rec.Code != 200 {
		t.Fatalf("member recovery: %d %s", rec.Code, rec.Body)
	}
	if got := securityCall(h, h.GetAuthSecurity, old, "GET", "/", "", nil); got.Code != 401 {
		t.Fatal("recovery retained old session")
	}
	if err := h.createSession(context.Background(), httptest.NewRecorder(), "member-a"); err == nil {
		t.Fatal("password bypassed authorized recovery link")
	}
	securityExec(t, h, `UPDATE server_settings SET signin_policy_json='{"admin_password_enabled":false,"members_sso_only":true,"microsoft_enabled":true}'`)
	raw, err := authsecurity.Recover(context.Background(), h.db, "owner", "server-cli", "server recovery", true, false)
	if err != nil {
		t.Fatal(err)
	}
	claim := func() *httptest.ResponseRecorder {
		out := httptest.NewRecorder()
		h.ClaimAuthRecovery(out, httptest.NewRequest("POST", "/", strings.NewReader(`{"token":"`+raw+`"}`)))
		return out
	}
	rec = claim()
	if rec.Code != 200 {
		t.Fatalf("owner recovery disabled by SSO policy: %d %s", rec.Code, rec.Body)
	}
	owner := securityCookie(t, rec, sessionCookieName)
	rec = securityCall(h, h.BeginTOTP, owner, "POST", "/", "{}", nil)
	if rec.Code != 200 {
		t.Fatalf("owner enrollment: %d %s", rec.Code, rec.Body)
	}
	var setup struct {
		Token  string `json:"setup_token"`
		Secret string `json:"secret"`
	}
	if err = json.Unmarshal(rec.Body.Bytes(), &setup); err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(setup.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rec = securityCall(h, h.ConfirmTOTP, owner, "POST", "/", `{"setup_token":"`+setup.Token+`","code":"`+code+`"}`, nil)
	if rec.Code != 200 {
		t.Fatalf("complete owner enrollment: %d %s", rec.Code, rec.Body)
	}
	emergency := securityCookie(t, rec, sessionCookieName)
	if emergency.MaxAge > 3600 {
		t.Fatalf("emergency session too long: %d", emergency.MaxAge)
	}
	if got := securityCall(h, h.GetMe, emergency, "GET", "/", "", nil); got.Code != 200 {
		t.Fatalf("enrolled owner access: %d %s", got.Code, got.Body)
	}
	securityExec(t, h, `UPDATE sessions SET created_at=? WHERE id=?`, time.Now().Add(-61*time.Minute).UTC().Format(time.RFC3339), emergency.Value)
	if got := securityCall(h, h.GetMe, emergency, "GET", "/", "", nil); got.Code != 401 {
		t.Fatal("owner emergency outlived one hour")
	}
	if got := claim(); got.Code != 401 {
		t.Fatal("owner recovery link reusable")
	}
	p, err := h.signinPolicy(context.Background())
	if err != nil || p.AdminPasswordEnabled {
		t.Fatal("emergency recovery globally enabled native sign-in")
	}
}

func TestNativeMFA_PasswordResetCannotBypassEnrolledFactor(t *testing.T) {
	h := securitySeed(t)
	securityTOTP(t, h, "member-a")
	hash, err := bcrypt.GenerateFromPassword([]byte("original-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	securityExec(t, h, `UPDATE users SET email_login=1,password_hash=? WHERE id='member-a'`, string(hash))
	raw := "password-recovery-test-token"
	securityExec(t, h, `INSERT INTO password_reset_tokens(token_hash,user_id,expires_at)VALUES(?,'member-a',?)`, authsecurity.Hash(raw), time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	old := securitySession(t, h, "member-a", true, false)
	rec := httptest.NewRecorder()
	h.ResetPassword(rec, httptest.NewRequest("POST", "/", strings.NewReader(`{"token":"`+raw+`","new_password":"replacement-password-123"}`)))
	if rec.Code != 200 || rec.Header().Get("X-Calnode-Auth-Step") != "mfa" {
		t.Fatalf("reset bypassed MFA: %d %s", rec.Code, rec.Body)
	}
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == sessionCookieName && cookie.Value != "" {
			t.Fatal("reset issued full session before MFA")
		}
	}
	if got := securityCall(h, h.GetMe, old, "GET", "/", "", nil); got.Code != 401 {
		t.Fatal("password reset retained old session")
	}
}

func TestNativeMFA_AdminRecoveryRevokesKeysAndRequiresEnrollment(t *testing.T) {
	h := securitySeed(t)
	securityExec(t, h, `UPDATE users SET is_admin=1 WHERE id IN('member-a','member-b')`)
	securityTOTP(t, h, "member-a")
	securityTOTP(t, h, "member-b")
	actor := securitySession(t, h, "member-a", true, false)
	securityExec(t, h, `INSERT INTO api_keys(id,user_id,name,key_hash)VALUES('old-key','member-b','old',?)`, authsecurity.Hash("old-key-secret"))
	rec := securityCall(h, h.ResetUserMFA, actor, "POST", "/", `{"reason":"IT-456 identity verified"}`, map[string]string{"id": "member-b"})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "/auth/recover#token=") {
		t.Fatalf("peer admin recovery: %d %s", rec.Code, rec.Body)
	}
	var keys, methods int
	_ = h.db.QueryRow(`SELECT COUNT(*) FROM api_keys WHERE user_id='member-b'`).Scan(&keys)
	_ = h.db.QueryRow(`SELECT COUNT(*) FROM user_totp WHERE user_id='member-b'`).Scan(&methods)
	if keys != 0 || methods != 0 {
		t.Fatal("recovery retained privileged credentials")
	}
	if err := h.createSession(context.Background(), httptest.NewRecorder(), "member-b"); err == nil {
		t.Fatal("password bypassed IT recovery")
	}
	securityExec(t, h, `UPDATE server_settings SET signin_policy_json='{"require_admin_mfa":true,"admin_password_enabled":true}'`)
	rec = securityCall(h, h.ResetUserMFA, actor, "POST", "/", `{"reason":"IT-456","disable":true}`, map[string]string{"id": "member-b"})
	if rec.Code != 400 {
		t.Fatalf("required MFA disabled: %d %s", rec.Code, rec.Body)
	}
}
func TestNativeMFA_HelpdeskCannotRecoverAdmins(t *testing.T) {
	h := securitySeed(t)
	securityExec(t, h, `INSERT INTO roles(id,name,permissions_json)VALUES('helpdesk','Helpdesk','{"members.reset_mfa":"all","profile.read":"own","profile.update":"own"}')`)
	securityExec(t, h, `UPDATE users SET custom_role_id='helpdesk' WHERE id='member-a'`)
	securityExec(t, h, `UPDATE users SET is_admin=1 WHERE id='member-b'`)
	securityTOTP(t, h, "member-a")
	cookie := securitySession(t, h, "member-a", true, false)
	rec := securityCall(h, h.ResetUserMFA, cookie, "POST", "/", `{"reason":"IT-123"}`, map[string]string{"id": "member-b"})
	if rec.Code != 403 {
		t.Fatalf("helpdesk recovered admin: %d %s", rec.Code, rec.Body)
	}
}
func TestPasskeys_EnforceOriginUVAndChallengeBinding(t *testing.T) {
	h := securitySeed(t)
	h.baseURL = "https://scheduler.example.com"
	cookie := securitySession(t, h, "member-a", false, false)
	rec := securityCall(h, h.BeginPasskeyRegistration, cookie, "POST", "/", "{}", nil)
	if rec.Code != 200 {
		t.Fatalf("begin: %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"userVerification":"required"`) || !strings.Contains(rec.Body.String(), `"residentKey":"required"`) {
		t.Fatal("passkey verification requirements missing")
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("POST", "/", strings.NewReader(`{}`))
		req.AddCookie(cookie)
		req.Header.Set("X-Auth-Challenge", out.Token)
		r := httptest.NewRecorder()
		h.RequireAuth(h.FinishPasskeyRegistration)(r, req)
		if r.Code != 400 {
			t.Fatalf("invalid registration accepted: %d", r.Code)
		}
	}
	h.baseURL = "http://public.example.com"
	if _, err := h.webAuthn(); err == nil {
		t.Fatal("passkeys allowed an insecure public origin")
	}
}
