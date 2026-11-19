package handler

import (
	"bytes"
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"

	ip "github.com/calnode/calnode/internal/identityprovider"
	"github.com/calnode/calnode/internal/netutil"
	"github.com/calnode/calnode/internal/secret"
	"github.com/calnode/calnode/internal/uid"
	oidc "github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/microsoft"
)

const providerStateCookie = "calnode_provider_state"
const ctxProviderIntent contextKey = "provider_intent"

type providerIntent struct {
	ProviderID     string `json:"provider_id"`
	Revision       int    `json:"revision"`
	PolicyRevision string `json:"policy_revision"`
	Nonce          string `json:"nonce"`
	Verifier       string `json:"verifier"`
	InviteHash     string `json:"invite_hash"`
	SessionID      string `json:"session_id"`
	LinkUser       string `json:"link_user"`
}

func isSSOMethod(method string) bool {
	return method == "google" || method == "microsoft" || strings.HasPrefix(method, "sso:")
}
func providerCallbackURL(base string, p ip.Provider) string {
	path := "/v1/auth/providers/" + p.ID + "/callback"
	if p.LegacyKey == "google" {
		path = "/v1/auth/callback"
	}
	if p.LegacyKey == "microsoft" {
		path = "/v1/auth/microsoft/callback"
	}
	return strings.TrimRight(base, "/") + path
}
func providerHTTPContext(ctx context.Context) context.Context {
	// Tests may supply an explicit transport. Production uses the same metadata
	// guard as other self-hosted integrations, with TLS verification and a timeout.
	if _, ok := ctx.Value(oauth2.HTTPClient).(*http.Client); ok {
		return ctx
	}
	hc := &http.Client{Timeout: 12 * time.Second, Transport: netutil.MetadataSafeTransport(slog.Default(), "identity provider HTTP"), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return oidc.ClientContext(ctx, hc)
}

type oidcRuntime struct {
	Config        *oauth2.Config
	Provider      *oidc.Provider
	MicrosoftKeys oidc.KeySet
}

func (h *Handler) providerRuntime(ctx context.Context, p ip.Provider) (oidcRuntime, error) {
	plain, err := secret.Decrypt(h.encKey, p.SecretEnc)
	if err != nil {
		return oidcRuntime{}, err
	}
	cfg := &oauth2.Config{ClientID: p.ClientID, ClientSecret: plain, RedirectURL: providerCallbackURL(h.baseURL, p), Scopes: []string{oidc.ScopeOpenID, "profile", "email"}}
	if p.Kind == "microsoft" {
		tenant := p.MicrosoftTenantID
		if tenant == "" {
			tenant = "common"
		}
		cfg.Endpoint = microsoft.AzureADEndpoint(tenant)
		cfg.Scopes = append(cfg.Scopes, "User.Read")
		return oidcRuntime{Config: cfg, MicrosoftKeys: oidc.NewRemoteKeySet(ctx, "https://login.microsoftonline.com/common/discovery/v2.0/keys")}, nil
	}
	issuer := p.IssuerURL
	if p.Kind == "google" {
		issuer = "https://accounts.google.com"
	}
	discovery, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return oidcRuntime{}, fmt.Errorf("provider discovery failed: %w", err)
	}
	cfg.Endpoint = discovery.Endpoint()
	var metadata struct {
		JWKS string `json:"jwks_uri"`
	}
	if discovery.Claims(&metadata) != nil {
		return oidcRuntime{}, fmt.Errorf("invalid discovery metadata")
	}
	for _, endpoint := range []string{cfg.Endpoint.AuthURL, cfg.Endpoint.TokenURL, metadata.JWKS} {
		u, e := url.Parse(endpoint)
		if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
			return oidcRuntime{}, fmt.Errorf("provider endpoints must use HTTPS without embedded credentials")
		}
	}
	return oidcRuntime{Config: cfg, Provider: discovery}, nil
}
func (h *Handler) setProviderState(w http.ResponseWriter, state string) {
	http.SetCookie(w, &http.Cookie{Name: providerStateCookie, Value: state, Path: "/", HttpOnly: true, Secure: h.secureCookie, SameSite: http.SameSiteLaxMode, MaxAge: 300})
}
func (h *Handler) consumeProviderIntent(w http.ResponseWriter, r *http.Request, purpose string) (providerIntent, error) {
	var intent providerIntent
	cookie, err := r.Cookie(providerStateCookie)
	state := r.URL.Query().Get("state")
	http.SetCookie(w, &http.Cookie{Name: providerStateCookie, Path: "/", HttpOnly: true, Secure: h.secureCookie, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	if err != nil || state == "" || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(state)) != 1 {
		return intent, fmt.Errorf("invalid sign-in state")
	}
	_, raw, err := h.consumeAuthChallenge(r.Context(), state, purpose)
	if err != nil {
		return intent, err
	}
	err = json.Unmarshal([]byte(raw), &intent)
	return intent, err
}

func (h *Handler) LoginIdentityProvider(w http.ResponseWriter, r *http.Request) {
	p, err := ip.Get(r.Context(), h.db, r.PathValue("id"))
	if err != nil || !p.SSO() {
		h.writeError(w, 404, "sign-in provider is unavailable")
		return
	}
	policy, err := h.signinPolicy(r.Context())
	if err != nil {
		h.writeError(w, 503, "sign-in policy unavailable")
		return
	}
	intent := providerIntent{ProviderID: p.ID, Revision: p.Revision, PolicyRevision: policy.revision, Nonce: oauth2.GenerateVerifier()}
	if p.PKCE {
		intent.Verifier = oauth2.GenerateVerifier()
	}
	if r.URL.Query().Get("link") == "1" {
		id, _, ok := h.sessionUser(r)
		cookie, e := r.Cookie(sessionCookieName)
		if !ok || e != nil {
			h.writeError(w, 401, "sign in before linking an identity")
			return
		}
		var user AuthUser
		user.ID = id
		user.SessionID = cookie.Value
		if !h.freshAuthFactor(w, r, user) {
			return
		}
		intent.LinkUser = id
		intent.SessionID = cookie.Value
	}
	if raw := r.URL.Query().Get("invite"); raw != "" {
		if intent.LinkUser != "" {
			h.writeError(w, 400, "linking and invitations are separate actions")
			return
		}
		intent.InviteHash = hashInviteToken(raw)
		var count int
		if h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM invite_tokens WHERE token_hash=? AND used_at IS NULL AND expires_at>?`, intent.InviteHash, time.Now().UTC().Format(time.RFC3339)).Scan(&count) != nil || count != 1 {
			h.writeError(w, 404, "invitation expired")
			return
		}
	}
	ctx := providerHTTPContext(r.Context())
	runtime, err := h.providerRuntime(ctx, p)
	if err != nil {
		h.logger.Warn("identity provider discovery", "provider_id", p.ID, "error", err)
		h.writeError(w, 503, "could not contact the sign-in provider; verify its issuer and configuration")
		return
	}
	state, err := h.authChallenge(r.Context(), "", "provider-login", intent)
	if err != nil {
		h.writeError(w, 500, "could not start sign-in")
		return
	}
	h.setProviderState(w, state)
	options := []oauth2.AuthCodeOption{oidc.Nonce(intent.Nonce), oauth2.SetAuthURLParam("prompt", "select_account")}
	if p.PKCE {
		options = append(options, oauth2.S256ChallengeOption(intent.Verifier))
	}
	http.Redirect(w, r, runtime.Config.AuthCodeURL(state, options...), http.StatusFound)
}

func (h *Handler) CallbackIdentityProvider(w http.ResponseWriter, r *http.Request) {
	intent, err := h.consumeProviderIntent(w, r, "provider-login")
	if err != nil {
		h.writeError(w, 400, "sign-in expired or state is invalid; start again")
		return
	}
	if id := r.PathValue("id"); id != "" && id != intent.ProviderID {
		h.writeError(w, 400, "sign-in provider mismatch")
		return
	}
	p, err := ip.Get(r.Context(), h.db, intent.ProviderID)
	policy, e := h.signinPolicy(r.Context())
	if err != nil || e != nil || !p.SSO() || p.Revision != intent.Revision || policy.revision != intent.PolicyRevision {
		h.writeError(w, 403, "provider or security policy changed; start sign-in again")
		return
	}
	if r.URL.Query().Get("error") != "" || r.URL.Query().Get("code") == "" {
		http.Redirect(w, r, "/admin/login?error=denied", 302)
		return
	}
	ctx := providerHTTPContext(r.Context())
	runtime, err := h.providerRuntime(ctx, p)
	if err != nil {
		h.writeError(w, 503, "could not verify the sign-in provider")
		return
	}
	options := []oauth2.AuthCodeOption{}
	if p.PKCE {
		options = append(options, oauth2.VerifierOption(intent.Verifier))
	}
	tok, err := runtime.Config.Exchange(ctx, r.URL.Query().Get("code"), options...)
	if err != nil {
		h.logger.Warn("identity provider exchange failed", "provider_id", p.ID, "error", err)
		h.writeError(w, 401, "sign-in code could not be verified")
		return
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok {
		h.writeError(w, 401, "provider did not return an ID token")
		return
	}
	verifierConfig := &oidc.Config{ClientID: p.ClientID, SupportedSigningAlgs: []string{"RS256", "RS384", "RS512", "PS256", "PS384", "PS512", "ES256", "ES384", "ES512", "EdDSA"}}
	var verified *oidc.IDToken
	if p.Kind == "microsoft" {
		// The tenant-independent Microsoft metadata contains an issuer template.
		// Select an exact, Microsoft-hosted tenant issuer, then verify the signature,
		// audience, expiry and that exact issuer. Never disable issuer validation.
		var untrusted struct {
			Issuer string `json:"iss"`
			Tenant string `json:"tid"`
		}
		parts := strings.Split(raw, ".")
		if len(parts) != 3 {
			h.writeError(w, 401, "invalid identity token")
			return
		}
		payload, decodeErr := base64.RawURLEncoding.DecodeString(parts[1])
		if decodeErr != nil || json.Unmarshal(payload, &untrusted) != nil {
			h.writeError(w, 401, "invalid identity token")
			return
		}
		tenant, te := uuid.Parse(untrusted.Tenant)
		if te != nil || tenant == uuid.Nil || untrusted.Issuer != "https://login.microsoftonline.com/"+tenant.String()+"/v2.0" {
			h.writeError(w, 401, "invalid Microsoft token issuer")
			return
		}
		configured := strings.ToLower(p.MicrosoftTenantID)
		if configured != "" && configured != "common" && configured != "organizations" && configured != "consumers" && configured != tenant.String() {
			h.writeError(w, 403, "Microsoft tenant is not allowed")
			return
		}
		if configured == "organizations" && tenant.String() == "9188040d-6c67-4c5b-b112-36a304b66dad" || configured == "consumers" && tenant.String() != "9188040d-6c67-4c5b-b112-36a304b66dad" {
			h.writeError(w, 403, "Microsoft account type is not allowed")
			return
		}
		verified, err = oidc.NewVerifier(untrusted.Issuer, runtime.MicrosoftKeys, verifierConfig).Verify(ctx, raw)
	} else {
		verified, err = runtime.Provider.Verifier(verifierConfig).Verify(ctx, raw)
	}
	if err != nil || verified.Subject == "" || subtle.ConstantTimeCompare([]byte(verified.Nonce), []byte(intent.Nonce)) != 1 {
		h.logger.Warn("identity token rejected", "provider_id", p.ID, "error", err)
		h.writeError(w, 401, "identity token or nonce could not be verified")
		return
	}
	claims := map[string]json.RawMessage{}
	if verified.Claims(&claims) != nil {
		h.writeError(w, 401, "invalid identity claims")
		return
	}
	var email, name, objectID string
	var emailVerified bool
	_ = json.Unmarshal(claims["email"], &email)
	_ = json.Unmarshal(claims["name"], &name)
	_ = json.Unmarshal(claims["email_verified"], &emailVerified)
	if p.Kind == "microsoft" {
		_ = json.Unmarshal(claims["preferred_username"], &email)
		_ = json.Unmarshal(claims["oid"], &objectID)
		emailVerified = !strings.Contains(strings.ToLower(email), "#ext#") && email != ""
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if len(name) > 160 {
		name = name[:160]
	}
	if err = h.completeProviderSignin(w, r, ctx, p, intent, verified.Issuer, verified.Subject, email, name, objectID, emailVerified, claims, runtime.Config, tok); err != nil {
		h.logger.Warn("provider sign-in refused", "provider_id", p.ID, "error", err)
		h.writeError(w, 403, err.Error())
	}
}

func (h *Handler) completeProviderSignin(w http.ResponseWriter, r *http.Request, ctx context.Context, p ip.Provider, intent providerIntent, issuer, subject, email, name, objectID string, emailVerified bool, claims map[string]json.RawMessage, cfg *oauth2.Config, tok *oauth2.Token) error {
	if email != "" {
		address, err := mail.ParseAddress(email)
		if err != nil || address.Address != email || len(email) > 254 {
			return fmt.Errorf("provider did not supply a valid email address")
		}
	}
	var userID, enrollment string
	var admin bool
	err := h.db.QueryRowContext(ctx, `SELECT u.id,u.enrollment_provider_id,(u.is_admin=1 OR u.is_owner=1) FROM user_identities i JOIN users u ON u.id=i.user_id WHERE i.provider_id=? AND i.issuer=? AND i.subject=? AND u.archived_at IS NULL`, p.ID, issuer, subject).Scan(&userID, &enrollment, &admin)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("could not load identity")
	}
	if intent.LinkUser != "" {
		cookie, e := r.Cookie(sessionCookieName)
		current, _, signed := h.sessionUser(r)
		if e != nil || !signed || cookie.Value != intent.SessionID || current != intent.LinkUser {
			return fmt.Errorf("sign in again before linking this identity")
		}
		if userID != "" && userID != current {
			return fmt.Errorf("this identity is already linked to another account")
		}
		userID = current
		if h.db.QueryRowContext(ctx, `SELECT enrollment_provider_id,(is_admin=1 OR is_owner=1) FROM users WHERE id=? AND archived_at IS NULL`, userID).Scan(&enrollment, &admin) != nil {
			return fmt.Errorf("account unavailable")
		}
	}
	// Restrict migration's email matching to a frozen, explicitly admitted legacy
	// account. New providers cannot silently take over an existing email address.
	if userID == "" && p.LegacyKey == "microsoft" && objectID != "" {
		_ = h.db.QueryRowContext(ctx, `SELECT id,enrollment_provider_id,(is_admin=1 OR is_owner=1) FROM users WHERE microsoft_subject=? AND archived_at IS NULL AND legacy_identity_eligible=1`, "microsoft:"+strings.ToLower(objectID)).Scan(&userID, &enrollment, &admin)
	}
	if userID == "" && p.LegacyKey != "" && emailVerified {
		var source, msSubject string
		e := h.db.QueryRowContext(ctx, `SELECT id,enrollment_provider_id,(is_admin=1 OR is_owner=1),enrollment_source,microsoft_subject FROM users WHERE email=? AND archived_at IS NULL AND legacy_identity_eligible=1`, email).Scan(&userID, &enrollment, &admin, &source, &msSubject)
		if e != nil {
			userID = ""
		} else if p.Kind == "microsoft" && msSubject != "" && msSubject != "microsoft:"+strings.ToLower(objectID) {
			return fmt.Errorf("Microsoft identity does not match this account")
		}
	}
	invited := intent.InviteHash != ""
	invitedAdmin := false
	if invited {
		_ = h.db.QueryRowContext(ctx, `SELECT role='admin' FROM invite_tokens WHERE token_hash=? AND used_at IS NULL AND expires_at>?`, intent.InviteHash, time.Now().UTC().Format(time.RFC3339)).Scan(&invitedAdmin)
	}
	if !admin && !invitedAdmin && !p.AllowMembers {
		return fmt.Errorf("this provider is reserved for administrators")
	}
	if enrollment != "" && enrollment != p.ID {
		return fmt.Errorf("sign in using the provider that enrolled your account")
	}
	needsEnrollment := userID == "" || enrollment == p.ID
	if !invited && needsEnrollment {
		if userID == "" && !p.AutoProvision {
			return fmt.Errorf("request an invitation or link this identity from an existing signed-in account")
		}
		if !emailVerified || !emailDomainAllowed(email, p.Domains) {
			return fmt.Errorf("email domain is not approved for automatic enrollment")
		}
		if !h.providerGroupsAllowed(ctx, p, claims, cfg, tok) {
			return fmt.Errorf("required group membership could not be verified")
		}
	}
	if invited && !emailVerified {
		return fmt.Errorf("the provider must verify the invited email address")
	}
	if userID == "" && !emailVerified {
		return fmt.Errorf("the provider must supply a verified email address")
	}
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("could not complete sign-in")
	}
	defer tx.Rollback()
	current, err := ip.Get(ctx, tx, p.ID)
	if err != nil || !current.SSO() || current.Revision != intent.Revision {
		return fmt.Errorf("provider changed; start again")
	}
	var policyRaw string
	if tx.QueryRowContext(ctx, `SELECT signin_policy_json FROM server_settings WHERE id=1`).Scan(&policyRaw) != nil || policyRaw != intent.PolicyRevision {
		return fmt.Errorf("security policy changed; start again")
	}
	if invited {
		if userID != "" {
			return fmt.Errorf("this invitation is for a new account; sign in with its existing linked identity")
		}
		var e error
		userID, e = h.claimProviderInvite(ctx, tx, intent.InviteHash, email, name, p.ID)
		if e != nil {
			return e
		}
	} else if userID == "" {
		var count int
		if tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count) != nil || count == 0 {
			return fmt.Errorf("an owner must claim the instance first")
		}
		userID = uid.New()
		if name == "" {
			name = email
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO users(id,email,name,iana_timezone,email_login,enrollment_source,enrollment_provider_id)VALUES(?,?,?,'UTC',0,'sso_provider',?)`, userID, email, name, p.ID); err != nil {
			return fmt.Errorf("an account already uses that email; link it from your existing account")
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO user_identities(provider_id,issuer,subject,user_id)VALUES(?,?,?,?) ON CONFLICT(provider_id,issuer,subject) DO NOTHING`, p.ID, issuer, subject, userID); err != nil {
		return fmt.Errorf("this account already has a different identity for this provider")
	}
	var bound string
	if tx.QueryRowContext(ctx, `SELECT user_id FROM user_identities WHERE provider_id=? AND issuer=? AND subject=?`, p.ID, issuer, subject).Scan(&bound) != nil || bound != userID {
		return fmt.Errorf("identity linking conflict")
	}
	if tx.Commit() != nil {
		return fmt.Errorf("could not save identity")
	}
	if intent.LinkUser != "" {
		h.securityAudit(ctx, userID, "identity.link", p.ID, nil)
		h.securityNotice(ctx, userID, "Sign-in identity linked", p.ID)
		http.Redirect(w, r, "/admin/settings/account-security?linked=true", 302)
		return nil
	}
	loginContext := context.WithValue(r.Context(), ctxMFAVerified, true)
	loginContext = context.WithValue(loginContext, ctxProviderIntent, intent)
	if err = h.createSessionMethod(loginContext, w, userID, "sso:"+p.ID); err != nil {
		return fmt.Errorf("account changed; sign in again")
	}
	h.securityAudit(ctx, userID, "identity.signin", p.ID, nil)
	if dest, ok := h.consumeOAuthReturn(w, r); ok {
		http.Redirect(w, r, dest, 302)
	} else {
		http.Redirect(w, r, "/admin", 302)
	}
	return nil
}

func (h *Handler) providerGroupsAllowed(ctx context.Context, p ip.Provider, claims map[string]json.RawMessage, cfg *oauth2.Config, tok *oauth2.Token) bool {
	if len(p.GroupIDs) == 0 {
		return true
	}
	var found []string
	if p.Kind == "microsoft" {
		body, _ := json.Marshal(map[string][]string{"groupIds": p.GroupIDs})
		req, err := http.NewRequestWithContext(ctx, "POST", "https://graph.microsoft.com/v1.0/me/checkMemberGroups", bytes.NewReader(body))
		if err != nil {
			return false
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := cfg.Client(ctx, tok).Do(req)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return false
		}
		var result struct {
			Value []string `json:"value"`
		}
		if json.NewDecoder(io.LimitReader(resp.Body, 16384)).Decode(&result) != nil {
			return false
		}
		found = result.Value
	} else if json.Unmarshal(claims[p.GroupsClaim], &found) != nil {
		return false
	}
	matches := 0
	for _, allowed := range p.GroupIDs {
		for _, group := range found {
			if group == allowed {
				matches++
				break
			}
		}
	}
	return p.GroupMatch == "all" && matches == len(p.GroupIDs) || p.GroupMatch == "any" && matches > 0
}

func (h *Handler) providerSessionAllowed(ctx context.Context, method, userID string) bool {
	if !h.identityProvidersEnabled(ctx) {
		return true
	}
	id := strings.TrimPrefix(method, "sso:")
	if method == "google" {
		id = "legacy-google"
	}
	if method == "microsoft" {
		id = "legacy-microsoft"
	}
	p, err := ip.Get(ctx, h.db, id)
	if err != nil || !p.SSO() {
		return false
	}
	var admin bool
	var enrollment string
	if h.db.QueryRowContext(ctx, `SELECT (is_admin=1 OR is_owner=1),enrollment_provider_id FROM users WHERE id=? AND archived_at IS NULL`, userID).Scan(&admin, &enrollment) != nil {
		return false
	}
	return (admin || p.AllowMembers) && (enrollment == "" || enrollment == p.ID)
}
