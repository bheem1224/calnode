package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/mail"
	"strings"

	"github.com/google/uuid"
)

// A missing policy preserves existing installations until an administrator opts in.
// Provider credentials configure calendar integrations independently of this policy.
type SigninPolicy struct {
	RequireAdminMFA      bool   `json:"require_admin_mfa"`
	RequireMemberMFA     bool   `json:"require_member_mfa"`
	PasswordMinLength    int    `json:"password_min_length"`
	SessionHours         int    `json:"session_hours"`
	DynamicLinkDays      int    `json:"dynamic_link_days"`
	revision             string // Original database value, used to reject login/policy-update races.
	GoogleEnabled        bool   `json:"google_enabled"`
	MicrosoftEnabled     bool   `json:"microsoft_enabled"`
	MembersSSOOnly       bool   `json:"members_sso_only"`
	AdminPasswordEnabled bool   `json:"admin_password_enabled"`
	AutoProvision        bool   `json:"auto_provision"`
	ApprovedDomains      string `json:"approved_domains"`
	RequiredGroupID      string `json:"required_group_id"`
}

func (h *Handler) signinPolicy(ctx context.Context) (SigninPolicy, error) {
	p := SigninPolicy{PasswordMinLength: 16, SessionHours: 12, DynamicLinkDays: 7, GoogleEnabled: true, MicrosoftEnabled: true, AdminPasswordEnabled: true}
	var raw string
	err := h.db.QueryRowContext(ctx, `SELECT signin_policy_json FROM server_settings WHERE id=1`).Scan(&raw)
	if err != nil {
		return p, err
	}
	if raw == "" {
		p.ApprovedDomains = strings.Join(h.getAllowedSignupDomains(), ",")
		p.AutoProvision = p.ApprovedDomains != ""
		return p, nil
	}
	err = json.Unmarshal([]byte(raw), &p)
	if p.PasswordMinLength == 0 {
		p.PasswordMinLength = 16
	}
	if p.SessionHours <= 0 {
		p.SessionHours = 12
	}
	if p.DynamicLinkDays <= 0 {
		p.DynamicLinkDays = 7
	}
	p.revision = raw
	return p, err
}

func (h *Handler) signinEnabled(ctx context.Context, provider string) bool {
	if h.identityProvidersEnabled(ctx) {
		var enabled bool
		var purpose string
		err := h.db.QueryRowContext(ctx, `SELECT enabled,purpose FROM identity_providers WHERE legacy_key=?`, provider).Scan(&enabled, &purpose)
		return err == nil && enabled && (purpose == "sso" || purpose == "both")
	}
	p, err := h.signinPolicy(ctx)
	if err != nil {
		return false
	}
	if provider == "google" {
		return p.GoogleEnabled
	}
	return p.MicrosoftEnabled
}

func (h *Handler) passwordAllowed(ctx context.Context, id string) bool {
	p, err := h.signinPolicy(ctx)
	if err != nil {
		return false
	}
	var admin bool
	if h.db.QueryRowContext(ctx, `SELECT (is_admin=1 OR is_owner=1) FROM users WHERE id=? AND archived_at IS NULL`, id).Scan(&admin) != nil {
		return false
	}
	if admin {
		return p.AdminPasswordEnabled
	}
	return !p.MembersSSOOnly
}

func policyAllowsSession(p SigninPolicy, method string, admin bool) bool {
	if strings.HasPrefix(method, "sso:") {
		return true
	}
	switch method {
	case "server_recovery":
		return admin
	case "microsoft":
		return p.MicrosoftEnabled
	case "google":
		return p.GoogleEnabled && (!p.MembersSSOOnly || admin)
	case "password", "legacy", "magic_link", "passkey":
		if admin {
			return p.AdminPasswordEnabled
		}
		return !p.MembersSSOOnly
	default:
		return false
	}
}

func (h *Handler) GetSigninPolicy(w http.ResponseWriter, r *http.Request) {
	p, err := h.signinPolicy(r.Context())
	if err != nil {
		h.writeError(w, 500, "could not load sign-in policy")
		return
	}
	h.writeJSON(w, 200, map[string]any{"policy": p, "google_configured": h.getGoogleAuth() != nil, "microsoft_configured": h.getMicrosoftAuth() != nil})
}

func (h *Handler) PutSigninPolicy(w http.ResponseWriter, r *http.Request) {
	u, _ := userFromContext(r.Context())
	if h.demoMode {
		h.writeError(w, 403, "security settings are read-only in demo mode")
		return
	}
	if !h.freshOwnershipSession(w, r, u) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var p SigninPolicy
	if json.NewDecoder(r.Body).Decode(&p) != nil {
		h.writeError(w, 400, "invalid JSON")
		return
	}
	if p.PasswordMinLength == 0 {
		p.PasswordMinLength = 16
	}
	if p.PasswordMinLength < 15 || p.PasswordMinLength > 128 {
		h.writeError(w, 400, "minimum password length must be between 15 and 128 characters")
		return
	}
	if p.SessionHours == 0 {
		p.SessionHours = 12
	}
	if p.DynamicLinkDays == 0 {
		p.DynamicLinkDays = 7
	}
	if p.SessionHours < 1 || p.SessionHours > 720 || p.DynamicLinkDays < 1 || p.DynamicLinkDays > 30 {
		h.writeError(w, 400, "session hours must be 1–720 and link expiry days 1–30")
		return
	}
	if p.RequireAdminMFA && !h.hasMFA(r.Context(), u.ID) {
		h.writeError(w, 400, "enroll a passkey or authenticator before requiring administrator MFA")
		return
	}
	p.RequiredGroupID = strings.ToLower(strings.TrimSpace(p.RequiredGroupID))
	domains := parseDomainsCSV(p.ApprovedDomains)
	for _, d := range domains {
		a, err := mail.ParseAddress("check@" + d)
		if err != nil || a.Address != "check@"+d || !strings.Contains(d, ".") {
			h.writeError(w, 400, "enter comma-separated company domains without @")
			return
		}
	}
	p.ApprovedDomains = strings.Join(domains, ",")
	if p.RequiredGroupID != "" {
		id, err := uuid.Parse(p.RequiredGroupID)
		if err != nil || id == uuid.Nil {
			h.writeError(w, 400, "security group must be a Microsoft group object ID")
			return
		}
		p.RequiredGroupID = id.String()
	}
	registry := h.identityProvidersEnabled(r.Context())
	ms := h.getMicrosoftAuth()
	if !registry {
		if p.MembersSSOOnly && (!p.MicrosoftEnabled || ms == nil) {
			h.writeError(w, 400, "configure and enable Microsoft sign-in before requiring it for Members")
			return
		}
		if p.AutoProvision && (!p.MicrosoftEnabled || ms == nil || len(domains) == 0 || p.RequiredGroupID == "") {
			h.writeError(w, 400, "automatic enrollment requires Microsoft sign-in, approved domains, and a security group")
			return
		}
		if p.RequiredGroupID != "" && (ms == nil || isMultiTenantMicrosoftAuthority(ms.Endpoint.AuthURL)) {
			h.writeError(w, 400, "configure a specific Microsoft tenant before using a security group")
			return
		}
	}
	if registry && p.MembersSSOOnly {
		var count int
		if h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM identity_providers WHERE enabled=1 AND allow_members=1 AND purpose IN('sso','both')`).Scan(&count) != nil || count == 0 {
			h.writeError(w, 400, "configure a sign-in provider for Members before requiring SSO")
			return
		}
	}
	var method string
	if h.db.QueryRowContext(r.Context(), `SELECT auth_method FROM sessions WHERE id=?`, u.SessionID).Scan(&method) != nil || !policyAllowsSession(p, method, u.InstanceAdmin || u.InstanceOwner) {
		h.writeError(w, 400, "sign in using a method that will remain enabled before saving")
		return
	}
	if !registry && !p.AdminPasswordEnabled && (!p.MicrosoftEnabled || ms == nil) && (!p.GoogleEnabled || h.getGoogleAuth() == nil) {
		h.writeError(w, 400, "keep at least one administrator sign-in method enabled")
		return
	}
	if registry && !p.AdminPasswordEnabled {
		var count int
		if h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM identity_providers WHERE enabled=1 AND purpose IN('sso','both')`).Scan(&count) != nil || count == 0 {
			h.writeError(w, 400, "keep an administrator sign-in method enabled")
			return
		}
	}
	var automaticUsers int
	if err := h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM users WHERE enrollment_source='microsoft_group' AND archived_at IS NULL`).Scan(&automaticUsers); err != nil {
		h.writeError(w, 503, "could not verify existing enrollment")
		return
	}
	if !registry && automaticUsers > 0 && p.MicrosoftEnabled && (len(domains) == 0 || p.RequiredGroupID == "") {
		h.writeError(w, 400, "keep approved domains and a security group for existing automatically enrolled accounts")
		return
	}
	raw, err := json.Marshal(p)
	if err != nil {
		h.writeError(w, 500, "could not save sign-in policy")
		return
	}
	previous, err := h.signinPolicy(r.Context())
	if err != nil {
		h.writeError(w, 503, "could not load sign-in policy")
		return
	}
	groupChanged := previous.RequiredGroupID != p.RequiredGroupID || previous.ApprovedDomains != p.ApprovedDomains
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.writeError(w, 500, "could not save sign-in policy")
		return
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err = tx.ExecContext(r.Context(), `UPDATE server_settings SET signin_policy_json=?,updated_at=datetime('now') WHERE id=1`, string(raw)); err == nil && !registry {
		// Unknown legacy sessions must authenticate again when enforcing SSO. Retain this verified administrator session.
		_, err = tx.ExecContext(r.Context(), `DELETE FROM sessions WHERE id<>? AND (auth_method='legacy' OR (?=1 AND auth_method<>'microsoft' AND user_id IN (SELECT id FROM users WHERE is_admin=0 AND is_owner=0)) OR (?=0 AND auth_method='google') OR (?=0 AND auth_method='microsoft') OR (?=0 AND auth_method IN ('password','magic_link')) OR (?=1 AND user_id IN (SELECT id FROM users WHERE enrollment_source='microsoft_group')))`, u.SessionID, p.MembersSSOOnly, p.GoogleEnabled, p.MicrosoftEnabled, p.AdminPasswordEnabled, groupChanged)
	}
	if err == nil && registry {
		_, err = tx.ExecContext(r.Context(), `DELETE FROM sessions WHERE id<>? AND (auth_method='legacy' OR (?=1 AND auth_method IN('password','magic_link','passkey') AND user_id IN(SELECT id FROM users WHERE is_admin=0 AND is_owner=0)) OR (?=0 AND auth_method IN('password','magic_link','passkey') AND user_id IN(SELECT id FROM users WHERE is_admin=1 OR is_owner=1)))`, u.SessionID, p.MembersSSOOnly, p.AdminPasswordEnabled)
	}

	if err != nil || tx.Commit() != nil {
		h.writeError(w, 500, "could not save sign-in policy")
		return
	}
	h.securityAudit(r.Context(), u.ID, "signin_policy.update", "instance", p)
	h.GetSigninPolicy(w, r)
}

// Explicit invitations are the enrollment-group exception; existing installations
// remain explicitly admitted rather than retroactively treating users as automatic.
func (h *Handler) requiresEnrollmentGroup(ctx context.Context, email string) (bool, error) {
	var source string
	err := h.db.QueryRowContext(ctx, `SELECT enrollment_source FROM users WHERE email=?`, email).Scan(&source)
	if err == sql.ErrNoRows {
		return true, nil
	}
	return source == "microsoft_group", err
}
