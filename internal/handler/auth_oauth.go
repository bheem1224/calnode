package handler

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"

	"github.com/calnode/calnode/internal/uid"
)

// newOAuthState generates a CSRF state token, sets it as a short-lived cookie, and
// returns it for inclusion in the provider's authorize URL. Shared by the Google
// and Microsoft sign-in flows.
func (h *Handler) newOAuthState(w http.ResponseWriter) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	state := hex.EncodeToString(b)
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- HttpOnly/SameSite/Secure are all set; Secure is h.secureCookie (dynamic on BASE_URL scheme), which gosec's static check can't verify
		Name:     stateCookieName,
		Value:    state,
		Path:     "/",
		MaxAge:   int(stateDuration.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   h.secureCookie,
	})
	return state, nil
}

// verifyOAuthState checks the ?state param against the state cookie and clears the
// cookie regardless (single use). Returns true when they match.
func (h *Handler) verifyOAuthState(w http.ResponseWriter, r *http.Request) bool {
	c, err := r.Cookie(stateCookieName)
	ok := err == nil && c.Value != "" && r.URL.Query().Get("state") == c.Value
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- HttpOnly/SameSite/Secure are all set; Secure is h.secureCookie (dynamic on BASE_URL scheme), which gosec's static check can't verify
		Name:     stateCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   h.secureCookie,
	})
	return ok
}

// finishOAuthLogin completes Google sign-in for an explicitly admitted account.
// Microsoft supplies a verified principal, group-check revision and object ID via
// the internal signinIdentity context before calling the shared completion path.
func (h *Handler) finishOAuthLogin(w http.ResponseWriter, r *http.Request, email string) {
	h.finishOAuthLoginWithJITEmail(w, r, email, email)
}

func (h *Handler) finishOAuthLoginWithJITEmail(w http.ResponseWriter, r *http.Request, email, jitEmail string) {
	identity, _ := r.Context().Value(ctxKeySigninIdentity).(signinIdentity)
	if identity.PolicyRevision != nil {
		current, err := h.signinPolicy(r.Context())
		if err != nil || current.revision != *identity.PolicyRevision {
			h.writeError(w, 403, "sign-in policy changed; please sign in again")
			return
		}
	}
	provider := identity.Provider
	if provider == "" {
		provider = "google"
	}
	policy, policyErr := h.signinPolicy(r.Context())
	if policyErr != nil {
		h.writeError(w, 503, "sign-in policy unavailable")
		return
	}
	if (provider == "google" && !policy.GoogleEnabled) || (provider == "microsoft" && !policy.MicrosoftEnabled) {
		h.writeError(w, 403, "this sign-in method is disabled")
		return
	}
	email = strings.ToLower(strings.TrimSpace(email))
	jitEmail = strings.ToLower(strings.TrimSpace(jitEmail))
	var userID string
	var archivedAt sql.NullString
	err := h.db.QueryRowContext(r.Context(),
		`SELECT id, archived_at FROM users WHERE email = ?`, email).Scan(&userID, &archivedAt)
	if errors.Is(err, sql.ErrNoRows) {
		// Never JIT-provision a non-owner member before the workspace has been claimed;
		// otherwise /v1/auth/claim would see userCount > 0 and lock out the first owner.
		var userCount int
		if cntErr := h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM users`).Scan(&userCount); cntErr != nil {
			h.logger.ErrorContext(r.Context(), "auth: count users", "error", cntErr)
			http.Redirect(w, r, "/admin/login?error=session", http.StatusFound)
			return
		}
		if userCount == 0 {
			h.logger.WarnContext(r.Context(), "auth: instance unclaimed, skipping JIT provisioning", "email", email)
			http.Redirect(w, r, "/admin/login?error=no_account", http.StatusFound)
			return
		}
		allowed := parseDomainsCSV(policy.ApprovedDomains)
		if !policy.AutoProvision || provider != "microsoft" {
			allowed = nil
		}
		if len(allowed) == 0 {
			h.logger.WarnContext(r.Context(), "auth: no account for email", "email", email)
			http.Redirect(w, r, "/admin/login?error=no_account", http.StatusFound)
			return
		}
		if !emailDomainAllowed(jitEmail, allowed) {
			h.logger.WarnContext(r.Context(), "auth: email domain not in allowed signup domains", "email", jitEmail)
			h.writeError(w, http.StatusForbidden, "email domain is not allowed")
			return
		}
		email = jitEmail
		userID = uid.New()
		name, _, _ := strings.Cut(email, "@")
		if name == "" {
			name = email
		}
		if _, insErr := h.db.ExecContext(r.Context(), `
			INSERT INTO users (id, email, name, iana_timezone, is_admin, is_owner, email_login,enrollment_source)
			VALUES (?, ?, ?, 'UTC', 0, 0, 0,'microsoft_group')`,
			userID, email, name); insErr != nil {
			// Handle concurrent login race if another request inserted the same email first.
			if reErr := h.db.QueryRowContext(r.Context(),
				`SELECT id, archived_at FROM users WHERE email = ?`, email).Scan(&userID, &archivedAt); reErr != nil {
				h.logger.ErrorContext(r.Context(), "auth: auto-provision user", "email", email, "error", insErr)
				http.Redirect(w, r, "/admin/login?error=session", http.StatusFound)
				return
			}
		} else {
			h.logger.InfoContext(r.Context(), "auth: auto-provisioned member user", "user_id", userID, "email", email)
		}
	} else if err != nil {
		h.logger.ErrorContext(r.Context(), "auth: lookup user", "email", email, "error", err)
		http.Redirect(w, r, "/admin/login?error=session", http.StatusFound)
		return
	}
	if archivedAt.Valid {
		http.Redirect(w, r, "/admin/login?error=archived", http.StatusFound)
		return
	}
	var admin bool
	if h.db.QueryRowContext(r.Context(), `SELECT (is_admin=1 OR is_owner=1) FROM users WHERE id=?`, userID).Scan(&admin) != nil || (policy.MembersSSOOnly && !admin && provider != "microsoft") {
		h.writeError(w, 403, "Members must sign in with Microsoft")
		return
	}
	if identity.Subject != "" {
		res, err := h.db.ExecContext(r.Context(), `UPDATE users SET microsoft_subject=? WHERE id=? AND (microsoft_subject='' OR microsoft_subject=?)`, identity.Subject, userID, identity.Subject)
		if err != nil {
			h.writeError(w, 403, "Microsoft identity does not match this account")
			return
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			h.writeError(w, 403, "Microsoft identity does not match this account")
			return
		}
	}
	if err := h.createSessionMethod(r.Context(), w, userID, provider); err != nil {
		h.logger.ErrorContext(r.Context(), "auth: create session", "error", err)
		http.Redirect(w, r, "/admin/login?error=session", http.StatusFound)
		return
	}
	// If this login was initiated by an MCP "Connect" flow, return to /oauth/authorize
	// (the consent step) rather than the admin home.
	if dest, ok := h.consumeOAuthReturn(w, r); ok {
		http.Redirect(w, r, dest, http.StatusFound) // #nosec G710 -- dest is validated by safeLocalPath (only "/oauth/authorize", never "//") both when set and when consumed; gosec's taint analysis can't trace through that check
		return
	}
	http.Redirect(w, r, "/admin", http.StatusFound)
}

func emailDomainAllowed(email string, allowed []string) bool {
	_, domain, ok := strings.Cut(strings.ToLower(strings.TrimSpace(email)), "@")
	if !ok || domain == "" {
		return false
	}
	for _, a := range allowed {
		if domain == a {
			return true
		}
	}
	return false
}

const ctxKeySigninIdentity contextKey = "signin_identity"

type signinIdentity struct {
	Provider, Subject, Email, InviteHash string
	PolicyRevision                       *string
}

func withSigninIdentity(r *http.Request, i signinIdentity) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), ctxKeySigninIdentity, i))
}
