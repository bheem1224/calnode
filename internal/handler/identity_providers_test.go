package handler

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	ip "github.com/calnode/calnode/internal/identityprovider"
	"github.com/calnode/calnode/internal/secret"
	jose "github.com/go-jose/go-jose/v4"
	"golang.org/x/oauth2"
)

func providerFixture(t *testing.T) (*Handler, ip.Provider) {
	t.Helper()
	h := newInternalHandler(t)
	seedSigninAdmin(t, h)
	if err := h.InitializeIdentityProviders(context.Background()); err != nil {
		t.Fatal(err)
	}
	encrypted, err := secret.Encrypt(h.encKey, "secret")
	if err != nil {
		t.Fatal(err)
	}
	p := ip.Provider{ID: "test-provider", Name: "Company", Kind: "oidc", Purpose: "sso", Enabled: true, AllowMembers: true, ClientID: "client", SecretEnc: encrypted, IssuerURL: "https://identity.example.com", PKCE: true, Domains: []string{"example.com"}, GroupIDs: []string{"staff", "it"}, GroupMatch: "any", GroupsClaim: "groups", AutoProvision: true, Revision: 1}
	tx, err := h.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = ip.Insert(context.Background(), tx, p); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return h, p
}

func TestProviderMigrationPreservesExistingApplications(t *testing.T) {
	h := newInternalHandler(t)
	seedSigninAdmin(t, h)
	h.SetGoogleAuth("google-client", "google-secret", "http://localhost/v1/auth/callback", false)
	h.SetMicrosoftAuth("ms-client", "ms-secret", testEnrollmentGroup, "http://localhost/v1/auth/microsoft/callback", false)
	setTestSigninPolicy(t, h, strictSigninPolicy())
	if _, err := h.db.Exec(`INSERT INTO users(id,email,name,enrollment_source)VALUES('enrolled','e@example.com','Enrolled','microsoft_group')`); err != nil {
		t.Fatal(err)
	}
	if err := h.InitializeIdentityProviders(context.Background()); err != nil {
		t.Fatal(err)
	}
	ps, err := h.listIdentityProviders(context.Background())
	if err != nil || len(ps) != 2 {
		t.Fatalf("providers %v %v", ps, err)
	}
	ms, err := ip.Get(context.Background(), h.db, "legacy-microsoft")
	if err != nil {
		t.Fatal(err)
	}
	if !ms.AutoProvision || len(ms.GroupIDs) != 1 || ms.GroupIDs[0] != testEnrollmentGroup || ms.MicrosoftTenantID != testEnrollmentGroup {
		t.Fatalf("migration lost enrollment: %+v", ms)
	}
	if providerCallbackURL("https://app", ms) != "https://app/v1/auth/microsoft/callback" {
		t.Fatal("changed registered callback")
	}
	var enrollment string
	_ = h.db.QueryRow(`SELECT enrollment_provider_id FROM users WHERE id='enrolled'`).Scan(&enrollment)
	if enrollment != ms.ID {
		t.Fatal("lost enrollment binding")
	}
	if err = h.InitializeIdentityProviders(context.Background()); err != nil {
		t.Fatal(err)
	}
	var count int
	_ = h.db.QueryRow(`SELECT COUNT(*) FROM identity_providers`).Scan(&count)
	if count != 2 {
		t.Fatal("import repeated")
	}
	raw, _ := json.Marshal(ms)
	if strings.Contains(string(raw), "ms-secret") || strings.Contains(string(raw), ms.SecretEnc) {
		t.Fatal("secret exposed")
	}
}

func TestProviderGroupRules(t *testing.T) {
	h, p := providerFixture(t)
	ctx := context.Background()
	for _, tc := range []struct {
		rule, claim string
		want        bool
	}{{"any", `["staff"]`, true}, {"all", `["staff"]`, false}, {"all", `["staff","it"]`, true}, {"any", `[]`, false}, {"any", `"staff"`, false}, {"any", `null`, false}} {
		p.GroupMatch = tc.rule
		if got := h.providerGroupsAllowed(ctx, p, map[string]json.RawMessage{"groups": json.RawMessage(tc.claim)}, nil, nil); got != tc.want {
			t.Fatalf("%s %s = %v", tc.rule, tc.claim, got)
		}
	}
	p.Kind = "microsoft"
	p.GroupIDs = []string{testEnrollmentGroup, "22222222-2222-4222-8222-222222222222"}
	p.GroupMatch = "any"
	client := &http.Client{Transport: signinRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://graph.microsoft.com/v1.0/me/checkMemberGroups" {
			t.Fatalf("unexpected URL %s", r.URL)
		}
		var body struct {
			IDs []string `json:"groupIds"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if len(body.IDs) != 2 {
			t.Fatal("lost groups")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"value":["` + testEnrollmentGroup + `"]}`)), Header: make(http.Header)}, nil
	})}
	ctx = context.WithValue(ctx, oauth2.HTTPClient, client)
	cfg := &oauth2.Config{}
	tok := &oauth2.Token{AccessToken: "access"}
	if !h.providerGroupsAllowed(ctx, p, nil, cfg, tok) {
		t.Fatal("ANY rejected")
	}
	p.GroupMatch = "all"
	if h.providerGroupsAllowed(ctx, p, nil, cfg, tok) {
		t.Fatal("ALL accepted partial")
	}
	p.MicrosoftTenantID = "common"
	if validateIdentityProvider(ctx, &p) == nil {
		t.Fatal("multitenant group checks accepted")
	}
	p.MicrosoftTenantID = testEnrollmentGroup
	p.GroupIDs = make([]string, 21)
	if validateIdentityProvider(ctx, &p) == nil {
		t.Fatal("unbounded group request")
	}
}

func TestProviderMigrationSeparatesMicrosoftSSOAndPersonalCalendar(t *testing.T) {
	h := newInternalHandler(t)
	seedSigninAdmin(t, h)
	h.SetMicrosoftAuth("company-client", "company-secret", testEnrollmentGroup, "https://app/v1/auth/microsoft/callback", false)
	encrypted, err := secret.Encrypt(h.encKey, "personal-secret")
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.db.Exec(`UPDATE server_settings SET microsoft_client_id='company-client',microsoft_client_secret_enc=?,microsoft_tenant_id=?,microsoft_allow_personal_calendars=1,microsoft_cal_client_id='personal-client',microsoft_cal_client_secret_enc=?`, encrypted, testEnrollmentGroup, encrypted)
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.db.Exec(`INSERT INTO calendar_connections(id,user_id,provider,account_email,calendar_id,access_token_enc)VALUES('original-grant','admin','microsoft','personal@example.com','primary','unchanged-encrypted-token')`)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.InitializeIdentityProviders(context.Background()); err != nil {
		t.Fatal(err)
	}
	sso, err := ip.Get(context.Background(), h.db, "legacy-microsoft")
	if err != nil {
		t.Fatal(err)
	}
	cal, err := ip.Get(context.Background(), h.db, "legacy-microsoft-calendar")
	if err != nil {
		t.Fatal(err)
	}
	if !sso.SSO() || sso.Integrations() || cal.SSO() || !cal.Integrations() || cal.ClientID != "personal-client" || cal.MicrosoftTenantID != "common" {
		t.Fatalf("apps mixed: %+v %+v", sso, cal)
	}
	if sso.Domains == nil || sso.GroupIDs == nil || cal.Domains == nil || cal.GroupIDs == nil {
		t.Fatal("provider editor requires empty arrays, not null")
	}
	var binding, token string
	_ = h.db.QueryRow(`SELECT oauth_provider_id,access_token_enc FROM calendar_connections WHERE id='original-grant'`).Scan(&binding, &token)
	if binding != cal.ID || token != "unchanged-encrypted-token" {
		t.Fatal("existing grant changed or bound to wrong app")
	}
	h.SetCalendar(nil)
	if err = h.InitializeIdentityProviders(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h.getCal() == nil || h.getCal().Provider("microsoft") == nil {
		t.Fatal("saved provider not available after restart")
	}
}

// Use a real signed ID token and HTTPS discovery/JWKS/token endpoints. The
// browser state, nonce and PKCE verifier travel through the production handlers.
func TestOIDCVerifiedSigninAndRejections(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", "test-key"))
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"valid", "bad-signature", "wrong-nonce", "wrong-audience", "wrong-issuer", "expired", "unverified-email", "missing-groups", "existing-email", "disabled", "policy-changed", "replay"} {
		t.Run(scenario, func(t *testing.T) {
			h, p := providerFixture(t)
			nonce, challenge := "", ""
			var issuer string
			mux := http.NewServeMux()
			srv := httptest.NewTLSServer(mux)
			defer srv.Close()
			issuer = srv.URL
			mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"issuer": issuer, "authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token", "jwks_uri": issuer + "/keys", "id_token_signing_alg_values_supported": []string{"RS256"}})
			})
			mux.HandleFunc("GET /keys", func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "test-key", Algorithm: "RS256", Use: "sig"}}})
			})
			mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
				_ = r.ParseForm()
				if oauth2.S256ChallengeFromVerifier(r.Form.Get("code_verifier")) != challenge {
					t.Error("PKCE verifier not bound")
				}
				claims := map[string]any{"iss": issuer, "aud": "client", "sub": "subject", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "nonce": nonce, "email": "new@example.com", "email_verified": true, "name": "New User", "groups": []string{"staff"}}
				switch scenario {
				case "wrong-nonce":
					claims["nonce"] = "wrong"
				case "wrong-audience":
					claims["aud"] = "another-app"
				case "wrong-issuer":
					claims["iss"] = "https://other.example.com"
				case "expired":
					claims["exp"] = time.Now().Add(-time.Hour).Unix()
				case "unverified-email":
					claims["email_verified"] = false
				case "missing-groups":
					delete(claims, "groups")
				case "existing-email":
					claims["email"] = "admin@example.com"
				}
				payload, _ := json.Marshal(claims)
				signed, _ := signer.Sign(payload)
				raw, _ := signed.CompactSerialize()
				if scenario == "bad-signature" {
					parts := strings.Split(raw, ".")
					replacement := "A"
					if strings.HasPrefix(parts[2], replacement) {
						replacement = "B"
					}
					parts[2] = replacement + parts[2][1:]
					raw = strings.Join(parts, ".")
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access", "token_type": "Bearer", "id_token": raw})
			})
			p.IssuerURL = issuer
			_, err = h.db.Exec(`UPDATE identity_providers SET issuer_url=? WHERE id=?`, issuer, p.ID)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.WithValue(context.Background(), oauth2.HTTPClient, srv.Client())
			req := httptest.NewRequest("GET", "/v1/auth/providers/test-provider/login", nil).WithContext(ctx)
			req.SetPathValue("id", p.ID)
			start := httptest.NewRecorder()
			h.LoginIdentityProvider(start, req)
			if start.Code != 302 {
				t.Fatalf("start %d %s", start.Code, start.Body)
			}
			location, _ := url.Parse(start.Header().Get("Location"))
			nonce = location.Query().Get("nonce")
			challenge = location.Query().Get("code_challenge")
			if challenge == "" || nonce == "" || location.Query().Get("code_challenge_method") != "S256" {
				t.Fatal("missing PKCE/nonce")
			}
			if scenario == "disabled" {
				_, _ = h.db.Exec(`UPDATE identity_providers SET enabled=0 WHERE id=?`, p.ID)
			}
			if scenario == "policy-changed" {
				_, _ = h.db.Exec(`UPDATE server_settings SET signin_policy_json='{}'`)
			}
			callback := func() *httptest.ResponseRecorder {
				req := httptest.NewRequest("GET", "/v1/auth/providers/test-provider/callback?code=code&state="+url.QueryEscape(location.Query().Get("state")), nil).WithContext(ctx)
				req.SetPathValue("id", p.ID)
				for _, cookie := range start.Result().Cookies() {
					req.AddCookie(cookie)
				}
				rec := httptest.NewRecorder()
				h.CallbackIdentityProvider(rec, req)
				return rec
			}
			rec := callback()
			success := scenario == "valid" || scenario == "replay"
			if success && rec.Code != 302 {
				t.Fatalf("callback %d %s", rec.Code, rec.Body)
			}
			if !success && rec.Code < 400 {
				t.Fatalf("unsafe callback accepted %s", scenario)
			}
			var count int
			_ = h.db.QueryRow(`SELECT COUNT(*) FROM user_identities`).Scan(&count)
			if success && count != 1 || !success && count != 0 {
				t.Fatalf("unexpected identity count %d", count)
			}
			if scenario == "replay" && callback().Code != 400 {
				t.Fatal("replayed callback accepted")
			}
		})
	}
}

func TestProviderEnrollmentBindingAndExplicitLink(t *testing.T) {
	h, p := providerFixture(t)
	ctx := context.Background()
	claims := map[string]json.RawMessage{"groups": json.RawMessage(`["staff"]`)}
	policy, _ := h.signinPolicy(ctx)
	intent := providerIntent{ProviderID: p.ID, Revision: p.Revision, PolicyRevision: policy.revision}
	run := func(p ip.Provider, email, subject string, intent providerIntent, r *http.Request) error {
		return h.completeProviderSignin(httptest.NewRecorder(), r, ctx, p, intent, p.IssuerURL, subject, email, "User", "", true, claims, nil, nil)
	}
	if err := run(p, "new@example.com", "subject", intent, httptest.NewRequest("GET", "/", nil)); err != nil {
		t.Fatal(err)
	}
	// An identity from an unrelated provider cannot bypass enrollment's next-login group check.
	other := p
	other.ID = "other"
	tx, _ := h.db.Begin()
	if err := ip.Insert(ctx, tx, other); err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit()
	if _, err := h.db.Exec(`INSERT INTO user_identities(provider_id,issuer,subject,user_id)SELECT ?,?,'other-subject',id FROM users WHERE email='new@example.com'`, other.ID, other.IssuerURL); err != nil {
		t.Fatal(err)
	}
	if err := run(other, "new@example.com", "other-subject", intent, httptest.NewRequest("GET", "/", nil)); err == nil {
		t.Fatal("cross-provider bypass")
	}
	if err := run(p, "admin@example.com", "attacker", intent, httptest.NewRequest("GET", "/", nil)); err == nil {
		t.Fatal("new provider took over matching email")
	}
	var id string
	_ = h.db.QueryRow(`SELECT id FROM users WHERE email='new@example.com'`).Scan(&id)
	if h.providerSessionAllowed(ctx, "sso:other", id) {
		t.Fatal("other provider session accepted")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := h.db.Exec(`INSERT INTO sessions(id,user_id,expires_at,reauthenticated_at,auth_method)VALUES('link-session','admin',?,?,'password')`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339), now)
	if err != nil {
		t.Fatal(err)
	}
	intent.LinkUser = "admin"
	intent.SessionID = "link-session"
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "link-session"})
	if err = run(p, "admin@example.com", "admin-subject", intent, req); err != nil {
		t.Fatalf("explicit linking failed: %v", err)
	}
}

func TestProviderCalendarOnlyCannotSignin(t *testing.T) {
	h, p := providerFixture(t)
	_, _ = h.db.Exec(`UPDATE identity_providers SET kind='microsoft',purpose='integrations',microsoft_tenant_id='common' WHERE id=?`, p.ID)
	req := httptest.NewRequest("GET", "/", nil)
	req.SetPathValue("id", p.ID)
	rec := httptest.NewRecorder()
	h.LoginIdentityProvider(rec, req)
	if rec.Code != 404 {
		t.Fatal("calendar-only provider offers login")
	}
	list, err := h.publicIdentityProviders(context.Background(), false)
	if err != nil || len(list) != 0 {
		t.Fatal("calendar-only app exposed for sign-in")
	}
}

func TestProviderMigrationDoesNotLeaveLegacyCallbackBypass(t *testing.T) {
	h, _ := providerFixture(t)
	for _, callback := range []http.HandlerFunc{h.CallbackGoogle, h.CallbackMicrosoft} {
		req := httptest.NewRequest("GET", "/callback?code=untrusted&state=forged", nil)
		req.AddCookie(&http.Cookie{Name: stateCookieName, Value: "forged"})
		rec := httptest.NewRecorder()
		callback(rec, req)
		if rec.Code != 400 {
			t.Fatalf("legacy state bypassed provider intents: %d %s", rec.Code, rec.Body)
		}
	}
}

func TestProviderMCPUsesConfiguredOIDCInsteadOfLegacyVendor(t *testing.T) {
	h, p := providerFixture(t)
	reg := httptest.NewRecorder()
	h.RegisterOAuthClient(reg, httptest.NewRequest("POST", "/oauth/register", strings.NewReader(`{"client_name":"Test AI","redirect_uris":["https://example.com/callback"]}`)))
	if reg.Code != 201 {
		t.Fatalf("registration %d %s", reg.Code, reg.Body)
	}
	var client struct {
		ID string `json:"client_id"`
	}
	_ = json.Unmarshal(reg.Body.Bytes(), &client)
	values := url.Values{"response_type": {"code"}, "client_id": {client.ID}, "redirect_uri": {"https://example.com/callback"}, "code_challenge": {"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, "code_challenge_method": {"S256"}, "scope": {"mcp"}, "state": {"client-state"}}
	req := httptest.NewRequest("GET", "/oauth/authorize?"+values.Encode(), nil)
	rec := httptest.NewRecorder()
	h.AuthorizeMCP(rec, req)
	if rec.Code != 302 || rec.Header().Get("Location") != "/v1/auth/providers/"+p.ID+"/login" {
		t.Fatalf("OIDC MCP sign-in %d %s", rec.Code, rec.Body)
	}
	var found bool
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == oauthReturnCookie {
			found = true
		}
	}
	if !found {
		t.Fatal("MCP consent return path lost")
	}
}

func TestProviderInvitesPreserveRoleAndEnrollmentException(t *testing.T) {
	for _, role := range []string{"member", "admin"} {
		t.Run(role, func(t *testing.T) {
			h, p := providerFixture(t)
			ctx := context.Background()
			if role == "admin" {
				p.AllowMembers = false
				_, _ = h.db.Exec(`UPDATE identity_providers SET allow_members=0 WHERE id=?`, p.ID)
			}
			_, link, _, err := h.issueInvite(ctx, "guest@outside.com", "Admin", "admin", role, "")
			if err != nil {
				t.Fatal(err)
			}
			parts := strings.Split(link, "/")
			raw := parts[len(parts)-1]
			policy, _ := h.signinPolicy(ctx)
			intent := providerIntent{ProviderID: p.ID, Revision: p.Revision, PolicyRevision: policy.revision, InviteHash: hashInviteToken(raw)}
			run := func(email string, intent providerIntent) error {
				return h.completeProviderSignin(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil), ctx, p, intent, p.IssuerURL, "guest-subject", email, "Guest", "", true, nil, nil, nil)
			}
			if run("wrong@outside.com", intent) == nil {
				t.Fatal("invitation accepted wrong verified address")
			}
			if err = run("guest@outside.com", intent); err != nil {
				t.Fatal(err)
			}
			var enrollment, source string
			var admin, local bool
			if err = h.db.QueryRow(`SELECT enrollment_provider_id,enrollment_source,is_admin,email_login FROM users WHERE email='guest@outside.com'`).Scan(&enrollment, &source, &admin, &local); err != nil {
				t.Fatal(err)
			}
			if enrollment != "" || source != "invite" || admin != (role == "admin") || local {
				t.Fatal("invitation role or exception lost")
			}
			intent.InviteHash = ""
			if err = run("guest@outside.com", intent); err != nil {
				t.Fatalf("exception lost on next sign-in: %v", err)
			}
			var used bool
			_ = h.db.QueryRow(`SELECT used_at IS NOT NULL FROM invite_tokens WHERE token_hash=?`, hashInviteToken(raw)).Scan(&used)
			if !used {
				t.Fatal("invitation not burned")
			}
		})
	}
}

func TestProviderSettingsRequireVerifiedInteractiveAdmin(t *testing.T) {
	h, p := providerFixture(t)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := h.db.Exec(`INSERT INTO sessions(id,user_id,expires_at,reauthenticated_at,auth_method)VALUES('verified','admin',?,?,'password')`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339), now)
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"name": "Personal calendars", "kind": "microsoft", "purpose": "integrations", "enabled": true, "allow_members": true, "client_id": "personal-client", "client_secret": "top-secret", "pkce": true, "group_match": "any", "approved_domains": []string{}, "group_ids": []string{}}
	save := func(method, id, session string) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(payload)
		req := httptest.NewRequest(method, "/v1/settings/identity-providers/"+id, strings.NewReader(string(raw)))
		req.SetPathValue("id", id)
		if session != "" {
			req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
		}
		rec := httptest.NewRecorder()
		h.RequireAuth(h.SaveIdentityProvider)(rec, req)
		return rec
	}
	if rec := save("POST", "", ""); rec.Code != 401 {
		t.Fatal("anonymous configuration allowed")
	}
	_, err = h.db.Exec(`INSERT INTO api_keys(id,user_id,name,key_hash,permissions_json)VALUES('admin-key','admin','Test',?,'{"settings.security":"all"}')`, hashAPIKey("test-admin-key"))
	if err != nil {
		t.Fatal(err)
	}
	keyReq := httptest.NewRequest("POST", "/v1/settings/identity-providers", strings.NewReader(`{}`))
	keyReq.Header.Set("Authorization", "Bearer test-admin-key")
	keyRec := httptest.NewRecorder()
	h.RequireAuth(h.SaveIdentityProvider)(keyRec, keyReq)
	if keyRec.Code != 403 {
		t.Fatalf("API key configured provider: %d", keyRec.Code)
	}
	rec := save("POST", "", "verified")
	if rec.Code != 200 {
		t.Fatalf("create %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "top-secret") {
		t.Fatal("returned plaintext secret")
	}
	var created ip.Provider
	if err = json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	stored, err := ip.Get(context.Background(), h.db, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.MicrosoftTenantID != "common" || !stored.Integrations() || stored.SSO() {
		t.Fatal("calendar application purpose lost")
	}
	payload["client_secret"] = ""
	payload["name"] = "Renamed"
	if rec = save("PUT", created.ID, "verified"); rec.Code != 200 {
		t.Fatalf("update %d %s", rec.Code, rec.Body)
	}
	again, _ := ip.Get(context.Background(), h.db, created.ID)
	if again.SecretEnc != stored.SecretEnc {
		t.Fatal("blank secret erased credentials")
	}
	_, err = h.db.Exec(`INSERT INTO calendar_connections(id,user_id,provider,account_email,calendar_id,access_token_enc,oauth_provider_id)VALUES('grant','admin','microsoft','admin@example.com','primary','opaque',?)`, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	payload["client_id"] = "another-app"
	if rec = save("PUT", created.ID, "verified"); rec.Code != 409 {
		t.Fatal("changed client for existing token")
	}
	_, _ = h.db.Exec(`UPDATE sessions SET reauthenticated_at=NULL WHERE id='verified'`)
	if rec = save("POST", "", "verified"); rec.Code != 403 {
		t.Fatal("configuration without fresh proof accepted")
	}
	_, _ = h.db.Exec(`UPDATE sessions SET reauthenticated_at=? WHERE id='verified'`, now)
	_, _ = h.db.Exec(`INSERT INTO users(id,email,name)VALUES('member','member@example.com','Member')`)
	_, _ = h.db.Exec(`INSERT INTO sessions(id,user_id,expires_at,reauthenticated_at,auth_method)VALUES('member-session','member',?,?,'password')`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339), now)
	if rec = save("POST", "", "member-session"); rec.Code != 403 {
		t.Fatal("Member configured provider")
	}
	setTestSigninPolicy(t, h, SigninPolicy{MembersSSOOnly: true, AdminPasswordEnabled: true, SessionHours: 12})
	_, _ = h.db.Exec(`UPDATE identity_providers SET kind='microsoft',issuer_url='',microsoft_tenant_id=? WHERE id=?`, testEnrollmentGroup, p.ID)
	payload = map[string]any{"name": p.Name, "kind": "microsoft", "purpose": "sso", "enabled": true, "allow_members": false, "client_id": p.ClientID, "client_secret": "", "microsoft_tenant_id": testEnrollmentGroup, "group_match": "any", "approved_domains": []string{}, "group_ids": []string{}}
	if rec = save("PUT", p.ID, "verified"); rec.Code != 400 {
		t.Fatalf("removed last Member provider %d %s", rec.Code, rec.Body)
	}
}
