package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

const testEnrollmentGroup = "11111111-1111-4111-8111-111111111111"

func setTestSigninPolicy(t *testing.T, h *Handler, p SigninPolicy) {
	t.Helper()
	raw, _ := json.Marshal(p)
	if _, err := h.db.Exec(`UPDATE server_settings SET signin_policy_json=? WHERE id=1`, string(raw)); err != nil {
		t.Fatal(err)
	}
}
func strictSigninPolicy() SigninPolicy {
	return SigninPolicy{MicrosoftEnabled: true, MembersSSOOnly: true, AdminPasswordEnabled: true, AutoProvision: true, ApprovedDomains: "example.com", RequiredGroupID: testEnrollmentGroup}
}
func seedSigninAdmin(t *testing.T, h *Handler) {
	t.Helper()
	if _, err := h.db.Exec(`INSERT INTO users(id,email,name,is_admin,is_owner,email_login,password_hash) VALUES('admin','admin@example.com','Admin',1,1,1,?)`, string(dummyHash)); err != nil {
		t.Fatal(err)
	}
}

type signinRoundTripper func(*http.Request) (*http.Response, error)

func (f signinRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func microsoftTestCallback(t *testing.T, h *Handler, email, objectID, groupResponse string, groupStatus int, invite string) (*httptest.ResponseRecorder, int) {
	t.Helper()
	h.SetMicrosoftAuth("client", "secret", "tenant", "http://localhost/v1/auth/microsoft/callback", false)
	starts := httptest.NewRecorder()
	startURL := "http://localhost/v1/auth/microsoft/login"
	if invite != "" {
		startURL += "?invite=" + invite
	}
	h.LoginMicrosoft(starts, httptest.NewRequest("GET", startURL, nil))
	if starts.Code != 302 {
		t.Fatalf("start %d %s", starts.Code, starts.Body.String())
	}
	cookie := starts.Result().Cookies()[0]
	groupCalls := 0
	client := &http.Client{Transport: signinRoundTripper(func(r *http.Request) (*http.Response, error) {
		status := 200
		body := ""
		switch {
		case strings.Contains(r.URL.Path, "/token"):
			body = `{"access_token":"test-access","token_type":"Bearer","expires_in":3600}`
		case strings.Contains(r.URL.Path, "checkMemberGroups"):
			groupCalls++
			status = groupStatus
			body = groupResponse
			if r.Header.Get("Authorization") != "Bearer test-access" {
				t.Error("group check missing delegated token")
			}
			payload, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(payload), testEnrollmentGroup) {
				t.Error("group ID missing")
			}
		case strings.Contains(r.URL.Path, "/me"):
			body = fmt.Sprintf(`{"id":%q,"mail":%q,"userPrincipalName":%q}`, objectID, email, email)
		default:
			t.Errorf("unexpected upstream %s", r.URL)
			status = 500
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	req := httptest.NewRequest("GET", "http://localhost/v1/auth/microsoft/callback?code=code&state="+cookie.Value, nil).WithContext(context.WithValue(context.Background(), oauth2.HTTPClient, client))
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.CallbackMicrosoft(rec, req)
	return rec, groupCalls
}

func TestMicrosoftEnrollmentPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, email, response string
		status, want          int
	}{
		{"approved domain and group", "person@example.com", `{"value":["` + testEnrollmentGroup + `"]}`, 200, 302},
		{"group absent", "person@example.com", `{"value":[]}`, 200, 403},
		{"Graph unavailable", "person@example.com", `{}`, 503, 503},
		{"invalid group response", "person@example.com", `broken`, 200, 503},
		{"wrong domain", "person@other.com", `{"value":["` + testEnrollmentGroup + `"]}`, 200, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newInternalHandler(t)
			seedSigninAdmin(t, h)
			setTestSigninPolicy(t, h, strictSigninPolicy())
			rec, _ := microsoftTestCallback(t, h, tc.email, "object-person", tc.response, tc.status, "")
			if rec.Code != tc.want {
				t.Fatalf("%d want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
			var count int
			_ = h.db.QueryRow(`SELECT COUNT(*) FROM users WHERE email=?`, tc.email).Scan(&count)
			if tc.want != 302 && count != 0 {
				t.Fatal("denied callback provisioned an account")
			}
			if tc.want == 302 {
				var source, method string
				if err := h.db.QueryRow(`SELECT u.enrollment_source,s.auth_method FROM users u JOIN sessions s ON s.user_id=u.id WHERE u.email=?`, tc.email).Scan(&source, &method); err != nil {
					t.Fatal(err)
				}
				if source != "microsoft_group" || method != "microsoft" {
					t.Fatalf("source/method %s/%s", source, method)
				}
				rec, calls := microsoftTestCallback(t, h, tc.email, "object-person", `{"value":[]}`, 200, "")
				if rec.Code != 403 || calls != 1 {
					t.Fatal("did not recheck group on subsequent sign-in")
				}
			}
		})
	}
}

func TestInvitedMemberMicrosoftException(t *testing.T) {
	h := newInternalHandler(t)
	seedSigninAdmin(t, h)
	setTestSigninPolicy(t, h, strictSigninPolicy())
	_, url, _, err := h.issueInvite(context.Background(), "guest@outside.com", "Admin", "admin", "member", "")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(url, "/")
	token := parts[len(parts)-1]
	request := httptest.NewRequest("POST", "/", strings.NewReader(`{"name":"Guest","password":"Valid-password-123"}`))
	request.SetPathValue("token", token)
	denied := httptest.NewRecorder()
	h.ClaimInvite(denied, request)
	if denied.Code != 403 {
		t.Fatalf("password bypass %d %s", denied.Code, denied.Body.String())
	}
	wrong, _ := microsoftTestCallback(t, h, "wrong@outside.com", "object-wrong", `{"value":[]}`, 200, token)
	if wrong.Code != 403 {
		t.Fatalf("wrong invited email accepted: %d", wrong.Code)
	}
	rec, calls := microsoftTestCallback(t, h, "guest@outside.com", "object-guest", `{}`, 503, token)
	if rec.Code != 302 || calls != 0 {
		t.Fatalf("invite %d, group calls %d: %s", rec.Code, calls, rec.Body.String())
	}
	var source, hash string
	var local int
	if err = h.db.QueryRow(`SELECT enrollment_source,email_login,COALESCE(password_hash,'') FROM users WHERE email='guest@outside.com'`).Scan(&source, &local, &hash); err != nil {
		t.Fatal(err)
	}
	if source != "invite" || local != 0 || hash != "" {
		t.Fatal("SSO invite created a password or lost enrollment source")
	}
	again, calls := microsoftTestCallback(t, h, "guest@outside.com", "object-guest", `{}`, 503, "")
	if again.Code != 302 || calls != 0 {
		t.Fatal("invitation exception lost on later sign-in")
	}
	start := httptest.NewRecorder()
	h.LoginMicrosoft(start, httptest.NewRequest("GET", "/v1/auth/microsoft/login?invite="+token, nil))
	if start.Code != 404 {
		t.Fatal("used invite remained usable")
	}
}

func TestSigninPolicySessionsAndPassword(t *testing.T) {
	h := newInternalHandler(t)
	seedSigninAdmin(t, h)
	setTestSigninPolicy(t, h, strictSigninPolicy())
	if _, err := h.db.Exec(`INSERT INTO users(id,email,name,email_login,password_hash) VALUES('member','member@example.com','Member',1,?)`, string(dummyHash)); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id, method string
		want       int
	}{{"admin", "password", 200}, {"member", "password", 401}, {"member", "legacy", 401}, {"member", "google", 401}, {"member", "microsoft", 200}} {
		t.Run(tc.id+tc.method, func(t *testing.T) {
			session := httptest.NewRecorder()
			if tc.method == "password" && tc.id == "member" || tc.method == "legacy" {
				token := "legacy-" + tc.id + tc.method
				_, err := h.db.Exec(`INSERT INTO sessions(id,user_id,expires_at,auth_method)VALUES(?,?,?,?)`, token, tc.id, time.Now().UTC().Add(time.Hour).Format(time.RFC3339), tc.method)
				if err != nil {
					t.Fatal(err)
				}
				http.SetCookie(session, &http.Cookie{Name: sessionCookieName, Value: token})
			} else if err := h.createSessionMethod(context.Background(), session, tc.id, tc.method); err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest("GET", "/v1/users/me", nil)
			req.AddCookie(session.Result().Cookies()[0])
			rec := httptest.NewRecorder()
			h.RequireAuth(h.GetMe)(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("%d want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
			_, _, ok := h.sessionUser(req)
			if ok != (tc.want == 200) {
				t.Fatal("MCP consent bypassed session policy")
			}
		})
	}
	for _, tc := range []struct {
		id   string
		want int
	}{{"member", 401}, {"admin", 200}} {
		rec := httptest.NewRecorder()
		h.LoginEmail(rec, httptest.NewRequest("POST", "/", strings.NewReader(fmt.Sprintf(`{"email":%q,"password":"dummy-constant-string"}`, tc.id+"@example.com"))))
		if rec.Code != tc.want {
			t.Fatalf("password %s status %d want %d", tc.id, rec.Code, tc.want)
		}
	}
	if _, err := h.db.Exec(`UPDATE users SET is_admin=0,is_owner=0 WHERE id='admin'`); err != nil {
		t.Fatal(err)
	}
	if h.passwordAllowed(context.Background(), "admin") {
		t.Fatal("demotion retained password access")
	}
}

func TestSigninPolicyValidationAndAuthorization(t *testing.T) {
	h := newInternalHandler(t)
	seedSigninAdmin(t, h)
	h.SetMicrosoftAuth("id", "secret", "tenant", "http://localhost/callback", false)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := h.db.Exec(`INSERT INTO sessions(id,user_id,expires_at,reauthenticated_at,auth_method)VALUES('verified','admin',?,?,'password')`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339), now)
	if err != nil {
		t.Fatal(err)
	}
	put := func(p SigninPolicy, session string) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(p)
		req := httptest.NewRequest("PUT", "/v1/settings/security", strings.NewReader(string(raw)))
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
		rec := httptest.NewRecorder()
		h.RequireAuth(h.PutSigninPolicy)(rec, req)
		return rec
	}
	p := strictSigninPolicy()
	p.AdminPasswordEnabled = false
	if rec := put(p, "verified"); rec.Code != 400 {
		t.Fatalf("allowed disabling current method: %d %s", rec.Code, rec.Body.String())
	}
	p = strictSigninPolicy()
	p.RequiredGroupID = ""
	if rec := put(p, "verified"); rec.Code != 400 {
		t.Fatal("missing group accepted")
	}
	p = strictSigninPolicy()
	if rec := put(p, "verified"); rec.Code != 200 {
		t.Fatalf("save %d %s", rec.Code, rec.Body.String())
	}
	var audit int
	_ = h.db.QueryRow(`SELECT COUNT(*) FROM security_audit_logs WHERE action='signin_policy.update'`).Scan(&audit)
	if audit != 1 {
		t.Fatal("missing audit")
	}
	_, _ = h.db.Exec(`UPDATE sessions SET reauthenticated_at=NULL WHERE id='verified'`)
	if rec := put(p, "verified"); rec.Code != 403 {
		t.Fatal("security update without recent verification")
	}
	_, _ = h.db.Exec(`INSERT INTO users(id,email,name)VALUES('ordinary','ordinary@example.com','Ordinary')`)
	memberSession := httptest.NewRecorder()
	_ = h.createSessionMethod(context.Background(), memberSession, "ordinary", "microsoft")
	if rec := put(p, memberSession.Result().Cookies()[0].Value); rec.Code != 403 {
		t.Fatal("Member modified policy")
	}
}

func TestGoogleSigninDisabledWithoutCalendarChanges(t *testing.T) {
	h := newInternalHandler(t)
	h.SetGoogleAuth("id", "secret", "http://localhost/callback", false)
	setTestSigninPolicy(t, h, strictSigninPolicy())
	for _, callback := range []bool{false, true} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/v1/auth/login", nil)
		if callback {
			h.CallbackGoogle(rec, req)
		} else {
			h.LoginGoogle(rec, req)
		}
		if rec.Code != 403 {
			t.Fatalf("disabled Google status %d", rec.Code)
		}
	}
	if h.getGoogleAuth() == nil {
		t.Fatal("policy cleared integration credentials")
	}
	h.SetAllowedSignupDomains([]string{"example.com"})
	p := strictSigninPolicy()
	p.GoogleEnabled = true
	setTestSigninPolicy(t, h, p)
	seedSigninAdmin(t, h)
	rec := httptest.NewRecorder()
	h.finishOAuthLogin(rec, httptest.NewRequest("GET", "/", nil), "stranger@example.com")
	var count int
	_ = h.db.QueryRow(`SELECT COUNT(*) FROM users WHERE email='stranger@example.com'`).Scan(&count)
	if count != 0 {
		t.Fatal("Google reused Microsoft enrollment policy")
	}
}

func TestSSOMemberCannotUsePreviouslyIssuedEmailTokens(t *testing.T) {
	h := newInternalHandler(t)
	seedSigninAdmin(t, h)
	_, err := h.db.Exec(`INSERT INTO users(id,email,name,email_login,password_hash) VALUES('member','member@example.com','Member',1,?)`, string(dummyHash))
	if err != nil {
		t.Fatal(err)
	}
	expiry := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	_, err = h.db.Exec(`INSERT INTO magic_link_tokens(token_hash,user_id,expires_at) VALUES(?,'member',?)`, hashInviteToken("magic"), expiry)
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.db.Exec(`INSERT INTO password_reset_tokens(token_hash,user_id,expires_at) VALUES(?,'member',?)`, hashInviteToken("reset"), expiry)
	if err != nil {
		t.Fatal(err)
	}
	setTestSigninPolicy(t, h, strictSigninPolicy())
	rec := httptest.NewRecorder()
	h.VerifyMagicLink(rec, httptest.NewRequest("GET", "/v1/auth/magic-link/verify?token=magic", nil))
	if !strings.Contains(rec.Header().Get("Location"), "error=link") {
		t.Fatal("old magic link bypassed policy")
	}
	rec = httptest.NewRecorder()
	h.ResetPassword(rec, httptest.NewRequest("POST", "/", strings.NewReader(`{"token":"reset","new_password":"Replacement-password-123"}`)))
	if rec.Code != 403 {
		t.Fatalf("old reset link bypassed policy: %d", rec.Code)
	}
	var sessions int
	_ = h.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id='member'`).Scan(&sessions)
	if sessions != 0 {
		t.Fatal("denied email token minted a session")
	}
}

func TestMicrosoftPolicyChangeDuringSigninDoesNotMintSession(t *testing.T) {
	h := newInternalHandler(t)
	seedSigninAdmin(t, h)
	setTestSigninPolicy(t, h, strictSigninPolicy())
	p, err := h.signinPolicy(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	identity := signinIdentity{Provider: "microsoft", PolicyRevision: &p.revision}
	req := withSigninIdentity(httptest.NewRequest("GET", "/", nil), identity)
	changed := strictSigninPolicy()
	changed.RequiredGroupID = "22222222-2222-4222-8222-222222222222"
	setTestSigninPolicy(t, h, changed)
	rec := httptest.NewRecorder()
	if err = h.createSessionMethod(req.Context(), rec, "admin", "microsoft"); err == nil {
		t.Fatal("stale group verification minted a session")
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("failed insert set a cookie")
	}
}

func TestInvitedAdminCanEstablishLocalFallback(t *testing.T) {
	h := newInternalHandler(t)
	seedSigninAdmin(t, h)
	setTestSigninPolicy(t, h, strictSigninPolicy())
	_, url, _, err := h.issueInvite(context.Background(), "newadmin@example.com", "Admin", "admin", "admin", "")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(url, "/")
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{"name":"New admin","password":"Administrative-password-123"}`))
	req.SetPathValue("token", parts[len(parts)-1])
	rec := httptest.NewRecorder()
	h.ClaimInvite(rec, req)
	if rec.Code != 201 {
		t.Fatalf("admin fallback %d: %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.LoginEmail(rec, httptest.NewRequest("POST", "/", strings.NewReader(`{"email":"newadmin@example.com","password":"Administrative-password-123"}`)))
	if rec.Code != 200 {
		t.Fatalf("invited admin could not use permitted fallback: %d", rec.Code)
	}
}

func TestSecurityReauthenticationHonorsLocalFallbackPolicy(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			h := newInternalHandler(t)
			seedSigninAdmin(t, h)
			p := strictSigninPolicy()
			p.AdminPasswordEnabled = enabled
			setTestSigninPolicy(t, h, p)
			created := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
			_, err := h.db.Exec(`INSERT INTO sessions(id,user_id,expires_at,created_at,auth_method) VALUES('old-sso','admin',?,?,'microsoft')`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339), created)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest("POST", "/v1/auth/reauthenticate", strings.NewReader(`{"password":"dummy-constant-string"}`))
			req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "old-sso"})
			rec := httptest.NewRecorder()
			h.RequireAuth(h.Reauthenticate)(rec, req)
			want := 403
			if enabled {
				want = 200
			}
			if rec.Code != want {
				t.Fatalf("%d want %d: %s", rec.Code, want, rec.Body.String())
			}
		})
	}
}
