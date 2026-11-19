package handler

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"strings"

	"github.com/calnode/calnode/internal/calendar"
	"github.com/calnode/calnode/internal/calendar/microsoft"
	"github.com/calnode/calnode/internal/gcal"
	ip "github.com/calnode/calnode/internal/identityprovider"
	"github.com/calnode/calnode/internal/secret"
	"github.com/calnode/calnode/internal/uid"
	"github.com/google/uuid"
)

func (h *Handler) identityProvidersEnabled(ctx context.Context) bool {
	var ready bool
	return h.db.QueryRowContext(ctx, `SELECT identity_providers_initialized FROM server_settings WHERE id=1`).Scan(&ready) == nil && ready
}

// InitializeIdentityProviders imports the effective legacy/env configuration once.
// Existing callback URLs, account admission, tokens and encryption remain intact.
func (h *Handler) InitializeIdentityProviders(ctx context.Context) error {
	if h.identityProvidersEnabled(ctx) {
		return h.ensureProviderCalendarBackends()
	}
	policy, err := h.signinPolicy(ctx)
	if err != nil {
		return err
	}
	var imported []ip.Provider
	makeProvider := func(id, name, kind, purpose, clientID, plain, tenant, legacy string) (ip.Provider, error) {
		encrypted, e := secret.Encrypt(h.encKey, plain)
		return ip.Provider{ID: id, Name: name, Kind: kind, Purpose: purpose, Enabled: true, AllowMembers: true, ClientID: clientID, SecretEnc: encrypted, MicrosoftTenantID: tenant, PKCE: true, Domains: []string{}, GroupIDs: []string{}, GroupMatch: "any", GroupsClaim: "groups", LegacyKey: legacy, Revision: 1}, e
	}
	if cfg := h.getGoogleAuth(); cfg != nil && cfg.ClientID != "" && cfg.ClientSecret != "" {
		purpose := "both"
		if !policy.GoogleEnabled {
			purpose = "integrations"
		}
		p, e := makeProvider("legacy-google", "Google", "google", purpose, cfg.ClientID, cfg.ClientSecret, "", "google")
		if e != nil {
			return e
		}
		p.AllowMembers = !policy.MembersSSOOnly
		imported = append(imported, p)
	}
	if cfg := h.getMicrosoftAuth(); cfg != nil && cfg.ClientID != "" && cfg.ClientSecret != "" {
		tenant := "common"
		if u, e := url.Parse(cfg.Endpoint.AuthURL); e == nil {
			parts := strings.Split(strings.Trim(u.Path, "/"), "/")
			if len(parts) > 0 {
				tenant = parts[0]
			}
		}
		purpose := "both"
		if !policy.MicrosoftEnabled {
			purpose = "integrations"
		}
		p, e := makeProvider("legacy-microsoft", "Microsoft / Entra ID", "microsoft", purpose, cfg.ClientID, cfg.ClientSecret, tenant, "microsoft")
		if e != nil {
			return e
		}
		p.AutoProvision = policy.AutoProvision
		p.Domains = parseDomainsCSV(policy.ApprovedDomains)
		if policy.RequiredGroupID != "" {
			p.GroupIDs = []string{policy.RequiredGroupID}
		}
		old, e := LoadMicrosoftSettingsFromDB(h.db, h.encKey)
		if e != nil {
			return e
		}
		if old != nil && old.AllowPersonalCalendars && old.CalClientID != "" && old.CalClientSecret != "" {
			p.Purpose = "sso"
			p.Enabled = policy.MicrosoftEnabled
			cal, e := makeProvider("legacy-microsoft-calendar", "Microsoft personal calendars", "microsoft", "integrations", old.CalClientID, old.CalClientSecret, "common", "")
			if e != nil {
				return e
			}
			imported = append(imported, cal)
		}
		imported = append(imported, p)
	}
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var ready bool
	if err = tx.QueryRowContext(ctx, `SELECT identity_providers_initialized FROM server_settings WHERE id=1`).Scan(&ready); err != nil {
		return err
	}
	if ready {
		return nil
	}
	for _, p := range imported {
		if err = ip.Insert(ctx, tx, p); err != nil {
			return err
		}
		if p.Integrations() {
			if _, err = tx.ExecContext(ctx, `UPDATE calendar_connections SET oauth_provider_id=? WHERE provider=? AND oauth_provider_id=''`, p.ID, p.Kind); err != nil {
				return err
			}
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE users SET enrollment_provider_id='legacy-microsoft' WHERE enrollment_source='microsoft_group'`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE server_settings SET identity_providers_initialized=1 WHERE id=1`); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return h.ensureProviderCalendarBackends()
}

func (h *Handler) listIdentityProviders(ctx context.Context) ([]ip.Provider, error) {
	rows, err := h.db.QueryContext(ctx, `SELECT `+ip.Columns+` FROM identity_providers ORDER BY created_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ip.Provider{}
	for rows.Next() {
		p, e := ip.Scan(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

type publicProvider struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
	URL  string `json:"url"`
}

func (h *Handler) publicIdentityProviders(ctx context.Context, members bool) ([]publicProvider, error) {
	all, err := h.listIdentityProviders(ctx)
	out := []publicProvider{}
	for _, p := range all {
		if p.SSO() && (!members || p.AllowMembers) {
			out = append(out, publicProvider{p.ID, p.Name, p.Kind, "/v1/auth/providers/" + p.ID + "/login"})
		}
	}
	return out, err
}
func (h *Handler) ListIdentityProviders(w http.ResponseWriter, r *http.Request) {
	ps, err := h.listIdentityProviders(r.Context())
	if err != nil {
		h.writeError(w, 500, "could not load providers")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	h.writeJSON(w, 200, map[string]any{"items": ps, "base_url": h.baseURL})
}
func validateIdentityProvider(ctx context.Context, p *ip.Provider) error {
	p.Name = strings.TrimSpace(p.Name)
	p.ClientID = strings.TrimSpace(p.ClientID)
	p.MicrosoftTenantID = strings.TrimSpace(p.MicrosoftTenantID)
	p.IssuerURL = strings.TrimSpace(p.IssuerURL)
	if p.Name == "" || len(p.Name) > 80 || p.ClientID == "" || len(p.ClientID) > 512 {
		return fmt.Errorf("enter a provider name and client ID")
	}
	if p.Kind != "google" && p.Kind != "microsoft" && p.Kind != "oidc" {
		return fmt.Errorf("choose Google, Microsoft or OIDC")
	}
	if p.Purpose != "sso" && p.Purpose != "both" && p.Purpose != "integrations" {
		return fmt.Errorf("choose a provider purpose")
	}
	if p.Kind == "oidc" && p.Purpose != "sso" {
		return fmt.Errorf("generic OIDC supports sign-in only")
	}
	if p.GroupMatch != "any" && p.GroupMatch != "all" {
		return fmt.Errorf("group matching must be any or all")
	}
	if len(p.GroupIDs) > 20 {
		return fmt.Errorf("choose at most 20 allowed groups")
	}
	seen := map[string]bool{}
	groups := []string{}
	for _, g := range p.GroupIDs {
		g = strings.TrimSpace(g)
		if g == "" || len(g) > 256 {
			return fmt.Errorf("enter valid group identifiers")
		}
		if p.Kind == "microsoft" {
			id, e := uuid.Parse(g)
			if e != nil || id == uuid.Nil {
				return fmt.Errorf("Microsoft groups must be group object IDs")
			}
			g = id.String()
		}
		if !seen[g] {
			groups = append(groups, g)
			seen[g] = true
		}
	}
	p.GroupIDs = groups
	domains := []string{}
	seen = map[string]bool{}
	for _, d := range p.Domains {
		d = strings.ToLower(strings.TrimSpace(d))
		a, e := mail.ParseAddress("check@" + d)
		if e != nil || a.Address != "check@"+d || !strings.Contains(d, ".") {
			return fmt.Errorf("enter domain names without @")
		}
		if !seen[d] {
			domains = append(domains, d)
			seen[d] = true
		}
	}
	p.Domains = domains
	if len(groups) > 0 && p.Kind == "google" {
		return fmt.Errorf("Google group enrollment is not supported; use an OIDC provider with group claims")
	}
	if p.Kind == "microsoft" {
		if p.MicrosoftTenantID == "" {
			p.MicrosoftTenantID = "common"
		}
		tenant := p.MicrosoftTenantID
		if tenant != "common" && tenant != "organizations" && tenant != "consumers" {
			id, e := uuid.Parse(tenant)
			if e != nil || id == uuid.Nil {
				return fmt.Errorf("enter a Microsoft tenant object ID, common, organizations or consumers")
			}
			p.MicrosoftTenantID = id.String()
		}
		if len(groups) > 0 && (tenant == "common" || tenant == "organizations" || tenant == "consumers") {
			return fmt.Errorf("Microsoft group checks require a specific tenant ID")
		}
	}
	if p.Kind == "oidc" {
		if err := validateBYOServerURL(ctx, p.IssuerURL, "issuer URL", "https"); err != nil {
			return err
		}
		u, _ := url.Parse(p.IssuerURL)
		if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("issuer URL cannot contain credentials, a query or a fragment")
		}
	}
	if p.GroupsClaim == "" {
		p.GroupsClaim = "groups"
	}
	if len(p.GroupsClaim) > 80 {
		return fmt.Errorf("group claim name is too long")
	}
	if p.AutoProvision && (len(domains) == 0 || p.Purpose == "integrations" || !p.AllowMembers) {
		return fmt.Errorf("automatic enrollment requires sign-in and approved domains")
	}
	if p.AutoProvision && p.Kind == "microsoft" && len(groups) == 0 {
		return fmt.Errorf("Microsoft automatic enrollment requires at least one security group")
	}
	return nil
}

func (h *Handler) SaveIdentityProvider(w http.ResponseWriter, r *http.Request) {
	u, _ := userFromContext(r.Context())
	if h.demoMode {
		h.writeError(w, 403, "provider settings are read-only in demo mode")
		return
	}
	if !h.freshAuthFactor(w, r, u) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	var in struct {
		ip.Provider
		ClientSecret *string `json:"client_secret"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		h.writeError(w, 400, "invalid JSON")
		return
	}
	id := r.PathValue("id")
	creating := id == ""
	var old ip.Provider
	var err error
	if creating {
		id = uid.New()
		in.LegacyKey = ""
		in.Revision = 1
	} else {
		old, err = ip.Get(r.Context(), h.db, id)
		if err != nil {
			h.writeError(w, 404, "provider not found")
			return
		}
		if in.Revision != 0 && in.Revision != old.Revision {
			h.writeError(w, 409, "provider changed; reload and retry")
			return
		}
		in.SecretEnc = old.SecretEnc
		in.LegacyKey = old.LegacyKey
		in.Revision = old.Revision + 1
	}
	in.ID = id
	if in.ClientSecret != nil && *in.ClientSecret != "" {
		in.SecretEnc, err = secret.Encrypt(h.encKey, *in.ClientSecret)
		if err != nil {
			h.writeError(w, 500, "could not encrypt provider secret")
			return
		}
	}
	if in.SecretEnc == "" {
		h.writeError(w, 400, "enter the provider client secret")
		return
	}
	if err = validateIdentityProvider(r.Context(), &in.Provider); err != nil {
		h.writeError(w, 400, err.Error())
		return
	}
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.writeError(w, 500, "could not save provider")
		return
	}
	defer tx.Rollback()
	if !creating {
		var refs int
		if err = tx.QueryRowContext(r.Context(), `SELECT (SELECT COUNT(*) FROM user_identities WHERE provider_id=?)+(SELECT COUNT(*) FROM calendar_connections WHERE oauth_provider_id=?)+(SELECT COUNT(*) FROM users WHERE enrollment_provider_id=?)`, id, id, id).Scan(&refs); err != nil {
			h.writeError(w, 500, "could not check provider usage")
			return
		}
		if refs > 0 && (old.Kind != in.Kind || old.ClientID != in.ClientID || old.IssuerURL != in.IssuerURL || old.MicrosoftTenantID != in.MicrosoftTenantID) {
			h.writeError(w, 409, "this provider has linked accounts; create another provider to change its identity or client ID")
			return
		}
		var enrolled int
		if tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM users WHERE enrollment_provider_id=? AND archived_at IS NULL`, id).Scan(&enrolled) != nil {
			h.writeError(w, 500, "could not check enrolled accounts")
			return
		}
		if enrolled > 0 && (len(in.Domains) == 0 || (len(old.GroupIDs) > 0 && len(in.GroupIDs) == 0)) {
			h.writeError(w, 400, "keep enrollment domains and groups for existing automatically enrolled accounts")
			return
		}
		var currentMethod string
		_ = tx.QueryRowContext(r.Context(), `SELECT auth_method FROM sessions WHERE id=?`, u.SessionID).Scan(&currentMethod)
		if (currentMethod == "sso:"+id || (old.LegacyKey != "" && currentMethod == old.LegacyKey)) && !in.SSO() {
			h.writeError(w, 400, "sign in using another method before disabling your current provider")
			return
		}
		policy, e := h.signinPolicyBeforeTx(r.Context(), tx)
		if e != nil {
			h.writeError(w, 500, "could not check sign-in policy")
			return
		}
		var others int
		_ = tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM identity_providers WHERE id<>? AND enabled=1 AND purpose IN('sso','both')`, id).Scan(&others)
		if !in.SSO() && others == 0 && (!policy.AdminPasswordEnabled || policy.MembersSSOOnly) {
			h.writeError(w, 400, "keep a sign-in provider enabled or update the instance sign-in policy first")
			return
		}
		var memberProviders int
		if tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM identity_providers WHERE id<>? AND enabled=1 AND allow_members=1 AND purpose IN('sso','both')`, id).Scan(&memberProviders) != nil {
			h.writeError(w, 500, "could not check Member sign-in")
			return
		}
		if policy.MembersSSOOnly && memberProviders == 0 && (!in.SSO() || !in.AllowMembers) {
			h.writeError(w, 400, "keep a sign-in provider available to Members while SSO is required")
			return
		}
		d, _ := json.Marshal(in.Domains)
		g, _ := json.Marshal(in.GroupIDs)
		res, e := tx.ExecContext(r.Context(), `UPDATE identity_providers SET name=?,kind=?,purpose=?,enabled=?,allow_members=?,client_id=?,client_secret_enc=?,microsoft_tenant_id=?,issuer_url=?,pkce=?,auto_provision=?,domains_json=?,group_ids_json=?,group_match=?,groups_claim=?,revision=revision+1 WHERE id=? AND revision=?`, in.Name, in.Kind, in.Purpose, in.Enabled, in.AllowMembers, in.ClientID, in.SecretEnc, in.MicrosoftTenantID, in.IssuerURL, in.PKCE, in.AutoProvision, string(d), string(g), in.GroupMatch, in.GroupsClaim, id, old.Revision)
		if e != nil {
			h.writeError(w, 500, "could not save provider")
			return
		}
		if n, _ := res.RowsAffected(); n != 1 {
			h.writeError(w, 409, "provider changed; reload and retry")
			return
		}
		// Retain the verified operator session. Other sessions reauthenticate using
		// the revised provider policy; stale in-flight callbacks also fail closed.
		if _, e = tx.ExecContext(r.Context(), `DELETE FROM sessions WHERE auth_method=? AND id<>?`, "sso:"+id, u.SessionID); e != nil {
			h.writeError(w, 500, "could not revoke outdated sessions")
			return
		}
	} else if err = ip.Insert(r.Context(), tx, in.Provider); err != nil {
		h.writeError(w, 500, "could not save provider")
		return
	}
	if tx.Commit() != nil {
		h.writeError(w, 500, "could not save provider")
		return
	}
	if err = h.ensureProviderCalendarBackends(); err != nil {
		h.logger.Error("provider calendar initialization", "error", err)
	}
	h.securityAudit(r.Context(), u.ID, "identity_provider.save", id, map[string]any{"kind": in.Kind, "purpose": in.Purpose, "enabled": in.Enabled})
	h.securityNotice(r.Context(), u.ID, "Identity provider configuration changed", id)
	in.SecretSet = true
	h.writeJSON(w, 200, in.Provider)
}

func (h *Handler) signinPolicyBeforeTx(ctx context.Context, tx *sql.Tx) (SigninPolicy, error) {
	p := SigninPolicy{AdminPasswordEnabled: true}
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT signin_policy_json FROM server_settings WHERE id=1`).Scan(&raw)
	if err == nil && raw != "" {
		err = json.Unmarshal([]byte(raw), &p)
	}
	return p, err
}

// Calendar backends continue to aggregate by vendor. Individual connection
// refreshes resolve their immutable OAuth app binding in identityprovider.Store.
func (h *Handler) ensureProviderCalendarBackends() error {
	ps, err := h.listIdentityProviders(context.Background())
	if err != nil {
		return err
	}
	svc := calendar.NewService(h.db)
	if old := h.getCal(); old != nil {
		for _, name := range old.ProviderNames() {
			svc.Register(old.Provider(name))
		}
	}
	for _, p := range ps {
		if !p.Integrations() || svc.Provider(p.Kind) != nil {
			continue
		}
		pr, e := h.calendarClient(p)
		if e != nil {
			return e
		}
		svc.Register(pr)
	}
	h.SetCalendar(svc)
	return nil
}
func (h *Handler) calendarClient(p ip.Provider) (calendar.Provider, error) {
	plain, err := secret.Decrypt(h.encKey, p.SecretEnc)
	if err != nil {
		return nil, err
	}
	key := hex.EncodeToString(h.encKey[:])
	redirect := strings.TrimRight(h.baseURL, "/") + "/v1/calendar/callback"
	if p.Kind == "google" {
		c, e := gcal.New(h.db, p.ClientID, plain, redirect, key)
		if e != nil {
			return nil, e
		}
		return c.WithProvider(p.ID), nil
	}
	if p.Kind == "microsoft" {
		c, e := microsoft.New(h.db, p.ClientID, plain, p.MicrosoftTenantID, redirect, key)
		if e != nil {
			return nil, e
		}
		return c.WithProvider(p.ID), nil
	}
	return nil, fmt.Errorf("this provider does not support calendar connections")
}
