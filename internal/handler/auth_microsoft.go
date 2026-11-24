package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/calnode/calnode/internal/providerhttp"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/microsoft"
)

// SetMicrosoftAuth configures Microsoft (Entra) OAuth sign-in. Identity only —
// openid/email/profile/User.Read; calendar access is a separate connection with its
// own scopes. Called from server.New when Microsoft credentials are configured.
// secure should be true when BASE_URL starts with https://.
func (h *Handler) SetMicrosoftAuth(clientID, clientSecret, tenant, redirectURL string, secure bool) {
	clientID = strings.TrimSpace(clientID)
	clientSecret = strings.TrimSpace(clientSecret)
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		tenant = "common"
	}
	var cfg *oauth2.Config
	if clientID != "" && clientSecret != "" {
		cfg = &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Endpoint:     microsoft.AzureADEndpoint(tenant),
			RedirectURL:  redirectURL,
			Scopes:       []string{"openid", "email", "profile", "User.Read"},
		}
	}
	h.authMu.Lock()
	h.microsoftAuth = cfg
	h.secureCookie = secure
	h.authMu.Unlock()
}

// LoginMicrosoft redirects to Microsoft's OAuth consent screen.
// GET /v1/auth/microsoft/login
func (h *Handler) LoginMicrosoft(w http.ResponseWriter, r *http.Request) {
	if h.identityProvidersEnabled(r.Context()) {
		r.SetPathValue("id", "legacy-microsoft")
		h.LoginIdentityProvider(w, r)
		return
	}
	ma := h.getMicrosoftAuth()
	if ma == nil {
		http.Error(w, "Microsoft OAuth not configured", http.StatusServiceUnavailable)
		return
	}
	if !h.signinEnabled(r.Context(), "microsoft") {
		h.writeError(w, 403, "Microsoft sign-in is disabled")
		return
	}
	state, err := h.newOAuthState(w)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "auth: generate state", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if raw := r.URL.Query().Get("invite"); raw != "" {
		var email string
		hash := hashInviteToken(raw)
		if h.db.QueryRowContext(r.Context(), `SELECT email FROM invite_tokens WHERE token_hash=? AND used_at IS NULL AND expires_at>?`, hash, time.Now().UTC().Format(time.RFC3339)).Scan(&email) != nil {
			h.writeError(w, 404, "invitation expired")
			return
		}
		if _, err := h.db.ExecContext(r.Context(), `INSERT INTO signin_intents(state_hash,invite_hash,expires_at) VALUES(?,?,?)`, hashInviteToken(state), hash, time.Now().UTC().Add(stateDuration).Format(time.RFC3339)); err != nil {
			h.writeError(w, 500, "could not start invitation sign-in")
			return
		}
	}
	// prompt=select_account so a cached SSO session can't silently pick the wrong one.
	http.Redirect(w, r, ma.AuthCodeURL(state, oauth2.AccessTypeOnline,
		oauth2.SetAuthURLParam("prompt", "select_account")), http.StatusFound)
}

// CallbackMicrosoft handles the OAuth redirect from Microsoft.
// GET /v1/auth/microsoft/callback
func (h *Handler) CallbackMicrosoft(w http.ResponseWriter, r *http.Request) {
	if h.identityProvidersEnabled(r.Context()) {
		r.SetPathValue("id", "legacy-microsoft")
		h.CallbackIdentityProvider(w, r)
		return
	}
	ma := h.getMicrosoftAuth()
	if ma == nil {
		http.Error(w, "Microsoft OAuth not configured", http.StatusServiceUnavailable)
		return
	}
	if !h.signinEnabled(r.Context(), "microsoft") {
		h.writeError(w, 403, "Microsoft sign-in is disabled")
		return
	}
	if !h.verifyOAuthState(w, r) {
		http.Redirect(w, r, "/admin/login?error=state", http.StatusFound)
		return
	}
	if r.URL.Query().Get("error") != "" {
		http.Redirect(w, r, "/admin/login?error=denied", http.StatusFound)
		return
	}

	tok, err := ma.Exchange(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		h.logger.ErrorContext(r.Context(), "auth: microsoft token exchange", "error", err)
		http.Redirect(w, r, "/admin/login?error=oauth", http.StatusFound)
		return
	}

	providerCtx := providerhttp.WithManager(r.Context(), providerhttp.ForDB(h.db))
	principal, objectID, err := fetchMicrosoftIdentity(providerCtx, ma, tok)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "auth: microsoft user info", "error", err)
		http.Redirect(w, r, "/admin/login?error=userinfo", http.StatusFound)
		return
	}
	// Match on the tenant-verified principal, never the mutable contact mail field.
	email, jitEmail := principal, principal
	identity := signinIdentity{Provider: "microsoft", Email: principal, Subject: "microsoft:" + strings.ToLower(objectID)}
	var boundEmail string
	if h.db.QueryRowContext(r.Context(), `SELECT email FROM users WHERE microsoft_subject=?`, identity.Subject).Scan(&boundEmail) == nil {
		email = boundEmail
	}
	err = h.db.QueryRowContext(r.Context(), `DELETE FROM signin_intents WHERE state_hash=? AND expires_at>? RETURNING invite_hash`, hashInviteToken(r.URL.Query().Get("state")), time.Now().UTC().Format(time.RFC3339)).Scan(&identity.InviteHash)
	if err != nil && err != sql.ErrNoRows {
		h.writeError(w, 503, "could not verify invitation")
		return
	}
	_, _ = h.db.ExecContext(r.Context(), `DELETE FROM signin_intents WHERE expires_at<=?`, time.Now().UTC().Format(time.RFC3339))
	if identity.InviteHash != "" {
		h.ClaimInvite(w, withSigninIdentity(r, identity))
		return
	}
	p, err := h.signinPolicy(r.Context())
	if err != nil {
		h.writeError(w, 503, "sign-in policy unavailable")
		return
	}
	identity.PolicyRevision = &p.revision
	needsGroup, err := h.requiresEnrollmentGroup(r.Context(), email)
	if err != nil {
		h.writeError(w, 503, "could not verify enrollment")
		return
	}
	if needsGroup {
		var existing int
		if h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM users WHERE email=?`, email).Scan(&existing) != nil {
			h.writeError(w, 503, "could not verify enrollment")
			return
		}
		if (existing == 0 && !p.AutoProvision) || !emailDomainAllowed(jitEmail, parseDomainsCSV(p.ApprovedDomains)) {
			h.writeError(w, 403, "automatic enrollment is not allowed for this account; request an invitation")
			return
		}
		if p.RequiredGroupID != "" {
			member, err := checkMicrosoftGroup(providerCtx, ma.Client(providerCtx, tok), p.RequiredGroupID, providerhttp.Key("microsoft", ma.ClientID, ma.Endpoint.TokenURL))
			if err != nil {
				h.logger.WarnContext(r.Context(), "Microsoft enrollment group check failed", "error", err)
				h.writeError(w, 503, "could not verify the enrollment security group; try again shortly")
				return
			}
			if !member {
				h.writeError(w, 403, "membership in the enrollment security group is required")
				return
			}
		}
	}
	h.finishOAuthLoginWithJITEmail(w, withSigninIdentity(r, identity), email, jitEmail)
}

func isMultiTenantMicrosoftAuthority(authURL string) bool {
	lower := strings.ToLower(authURL)
	return strings.Contains(lower, "/common/") ||
		strings.Contains(lower, "/organizations/") ||
		strings.Contains(lower, "/consumers/")
}

// fetchMicrosoftIdentity obtains the principal and stable directory object ID
// together using the delegated Graph token from this authorization-code exchange.
func fetchMicrosoftIdentity(ctx context.Context, cfg *oauth2.Config, tok *oauth2.Token) (principal, objectID string, err error) {
	app := providerhttp.Key("microsoft", cfg.ClientID, cfg.Endpoint.TokenURL)
	client := providerhttp.FromContext(ctx).Coordinate(cfg.Client(ctx, tok), app, providerhttp.Key(app, "signin"))
	resp, err := client.Get("https://graph.microsoft.com/v1.0/me?$select=id,userPrincipalName")
	if err != nil {
		return "", "", fmt.Errorf("auth: Graph identity request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("auth: Graph identity status %d", resp.StatusCode)
	}
	var info struct {
		ID        string `json:"id"`
		Principal string `json:"userPrincipalName"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 16384)).Decode(&info); err != nil {
		return "", "", fmt.Errorf("auth: decode Graph identity: %w", err)
	}
	principal = strings.ToLower(strings.TrimSpace(info.Principal))
	if principal == "" || info.ID == "" || strings.Contains(principal, "#ext#") {
		return "", "", fmt.Errorf("auth: Microsoft principal is missing or is a B2B guest identity")
	}
	return principal, info.ID, nil
}

// Microsoft documents User.Read as sufficient for the signed-in user's own
// transitive membership check. No directory-wide read permission is requested.
func checkMicrosoftGroup(ctx context.Context, client *http.Client, groupID string, appScopes ...string) (bool, error) {
	body, _ := json.Marshal(map[string]any{"groupIds": []string{groupID}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://graph.microsoft.com/v1.0/me/checkMemberGroups", bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req = providerhttp.ReplaySafe(req)
	app := "microsoft-signin-groups"
	if len(appScopes) > 0 {
		app = appScopes[0]
	}
	client = providerhttp.FromContext(ctx).Coordinate(client, app, providerhttp.Key(app, "signin"))
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return false, fmt.Errorf("Graph group check status %d", resp.StatusCode)
	}
	var result struct {
		Value []string `json:"value"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 16384)).Decode(&result); err != nil {
		return false, err
	}
	for _, id := range result.Value {
		if strings.EqualFold(id, groupID) {
			return true, nil
		}
	}
	return false, nil
}
