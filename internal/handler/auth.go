package handler

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/access"
)

type contextKey string

const ctxKeyUser contextKey = "user"

// AuthUser is the authenticated caller stored in request context.
type AuthUser struct {
	RecoveryOnly  bool
	Permissions   access.Grants
	CredentialID  string
	InstanceAdmin bool
	InstanceOwner bool
	Operation     string
	ID            string
	Email         string
	Name          string
	IANATZ        string
	TimeFormat    string // "12h" or "24h"
	WeekStart     int    // 0=Sunday, 1=Monday
	DateFormat    string // "dmy", "mdy", or "ymd"
	AvatarURL     string
	IsAdmin       bool
	IsOwner       bool

	// Granular RBAC & impersonation state
	CustomRoleID      string
	CustomRoleName    string
	CanImpersonate    bool
	CanManageUsers    bool
	CanManageSettings bool
	CanManageTeams    bool
	SessionID         string
	ImpersonatorID    string

	// Notification preferences (all default true).
	NotifyConfirmation   bool
	NotifyCancellation   bool
	NotifyReschedule     bool
	NotifyReminder       bool
	NotifyHostBooking    bool
	NotifyHostCancel     bool
	NotifyHostReschedule bool
}

func userFromContext(ctx context.Context) (AuthUser, bool) {
	u, ok := ctx.Value(ctxKeyUser).(AuthUser)
	return u, ok
}

// Role returns the workspace role string ("owner" | "admin" | "member")
// derived from the is_owner / is_admin flags. Owner implies admin.
func (u AuthUser) Role() string {
	switch {
	case u.IsOwner || u.InstanceOwner:
		return "owner"
	case u.IsAdmin:
		return "admin"
	default:
		return "member"
	}
}

// RequireAuth wraps a handler with authentication. It accepts either:
//   - An API key via X-API-Key header or Authorization: Bearer (for programmatic/MCP callers)
//   - A session cookie set by Google OAuth login (for the admin UI browser sessions)
//
// If a key header is present but invalid, the request is rejected immediately
// (no session fallback) to prevent confused-deputy attacks.
func (h *Handler) RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// --- API key path ---
		if key := extractAPIKey(r); key != "" {
			hash := hashAPIKey(key)
			var user AuthUser
			var keyID string
			var nc, nca, nr, nrm, nhb, nhc, nhr int
			var allowImp, canUsers, canSettings, canTeams sql.NullInt64
			err := h.db.QueryRowContext(r.Context(), `
				SELECT ak.id, u.id, u.email, u.name, u.iana_timezone, u.time_format, u.week_start, u.date_format, COALESCE(u.avatar_url,''), u.is_admin, u.is_owner,
				       COALESCE(u.notify_confirmation,1), COALESCE(u.notify_cancellation,1), COALESCE(u.notify_reschedule,1), COALESCE(u.notify_reminder,1),
				       COALESCE(u.notify_host_booking,1), COALESCE(u.notify_host_cancel,1), COALESCE(u.notify_host_reschedule,1),
				       COALESCE(u.custom_role_id,''), COALESCE(r.name,''),
				       r.allow_impersonation, r.can_manage_users, r.can_manage_settings, r.can_manage_teams
				FROM api_keys ak
				JOIN users u ON u.id = ak.user_id
				LEFT JOIN roles r ON r.id = u.custom_role_id
				WHERE ak.key_hash = ? AND u.archived_at IS NULL`, hash).
				Scan(&keyID, &user.ID, &user.Email, &user.Name, &user.IANATZ, &user.TimeFormat, &user.WeekStart, &user.DateFormat, &user.AvatarURL, &user.IsAdmin, &user.IsOwner,
					&nc, &nca, &nr, &nrm, &nhb, &nhc, &nhr,
					&user.CustomRoleID, &user.CustomRoleName, &allowImp, &canUsers, &canSettings, &canTeams)
			user.NotifyConfirmation, user.NotifyCancellation, user.NotifyReschedule, user.NotifyReminder = nc != 0, nca != 0, nr != 0, nrm != 0
			user.NotifyHostBooking, user.NotifyHostCancel, user.NotifyHostReschedule = nhb != 0, nhc != 0, nhr != 0
			user.CanImpersonate = user.IsOwner || user.IsAdmin || (allowImp.Valid && allowImp.Int64 != 0)
			user.CanManageUsers = user.IsOwner || user.IsAdmin || (canUsers.Valid && canUsers.Int64 != 0)
			user.CanManageSettings = user.IsOwner || user.IsAdmin || (canSettings.Valid && canSettings.Int64 != 0)
			user.CanManageTeams = user.IsOwner || user.IsAdmin || (canTeams.Valid && canTeams.Int64 != 0)
			if err != nil {
				h.writeError(w, http.StatusUnauthorized, "invalid API key")
				return
			}
			now := time.Now().UTC().Format(time.RFC3339Nano)
			_, _ = h.db.ExecContext(r.Context(),
				`UPDATE api_keys SET last_used_at = ? WHERE id = ?`, now, keyID)
			if err := h.loadAuthorization(r.Context(), &user, keyID); err != nil {
				h.writeError(w, 401, "invalid or expired API key")
				return
			}
			h.authorizedCall(w, r, user, next)
			return
		}

		// --- Session cookie path (admin browser UI) ---
		if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
			now := time.Now().UTC().Format(time.RFC3339)
			var user AuthUser
			var nc, nca, nr, nrm, nhb, nhc, nhr int
			var allowImp, canUsers, canSettings, canTeams sql.NullInt64
			var authMethod string
			if err := h.db.QueryRowContext(r.Context(), `
				SELECT s.id, COALESCE(s.impersonator_user_id,''),s.auth_method,s.recovery_only,
				       u.id, u.email, u.name, u.iana_timezone, u.time_format, u.week_start, u.date_format, COALESCE(u.avatar_url,''), u.is_admin, u.is_owner,
				       COALESCE(u.notify_confirmation,1), COALESCE(u.notify_cancellation,1), COALESCE(u.notify_reschedule,1), COALESCE(u.notify_reminder,1),
				       COALESCE(u.notify_host_booking,1), COALESCE(u.notify_host_cancel,1), COALESCE(u.notify_host_reschedule,1),
				       COALESCE(u.custom_role_id,''), COALESCE(r.name,''),
				       r.allow_impersonation, r.can_manage_users, r.can_manage_settings, r.can_manage_teams
				FROM sessions s
				JOIN users u ON u.id = s.user_id
				LEFT JOIN roles r ON r.id = u.custom_role_id
				WHERE s.id = ? AND s.expires_at > ? AND u.archived_at IS NULL`,
				cookie.Value, now).
				Scan(&user.SessionID, &user.ImpersonatorID, &authMethod, &user.RecoveryOnly,
					&user.ID, &user.Email, &user.Name, &user.IANATZ, &user.TimeFormat, &user.WeekStart, &user.DateFormat, &user.AvatarURL, &user.IsAdmin, &user.IsOwner,
					&nc, &nca, &nr, &nrm, &nhb, &nhc, &nhr,
					&user.CustomRoleID, &user.CustomRoleName, &allowImp, &canUsers, &canSettings, &canTeams); err == nil {
				if user.RecoveryOnly {
					allowed := map[string]bool{"GetAuthSecurity": true, "BeginTOTP": true, "ConfirmTOTP": true, "BeginPasskeyRegistration": true, "FinishPasskeyRegistration": true}
					if !allowed[handlerName(next)] {
						h.writeError(w, 403, "complete authentication setup before accessing the application")
						return
					}
				}
				p, policyErr := h.signinPolicy(r.Context())
				if user.ImpersonatorID != "" && !h.impersonationSessionValid(r.Context(), user, authMethod, p) {
					h.writeError(w, 401, "support session is no longer authorized")
					return
				}
				if policyErr != nil || (user.ImpersonatorID == "" && (!policyAllowsSession(p, authMethod, user.IsAdmin || user.IsOwner) || !h.sessionSecurityValid(r.Context(), user.SessionID, user.ID, authMethod, p, user.RecoveryOnly))) {
					h.writeError(w, 401, "sign in again using an allowed method")
					return
				}
				user.NotifyConfirmation, user.NotifyCancellation, user.NotifyReschedule, user.NotifyReminder = nc != 0, nca != 0, nr != 0, nrm != 0
				user.NotifyHostBooking, user.NotifyHostCancel, user.NotifyHostReschedule = nhb != 0, nhc != 0, nhr != 0
				user.CanImpersonate = user.IsOwner || user.IsAdmin || (allowImp.Valid && allowImp.Int64 != 0)
				user.CanManageUsers = user.IsOwner || user.IsAdmin || (canUsers.Valid && canUsers.Int64 != 0)
				user.CanManageSettings = user.IsOwner || user.IsAdmin || (canSettings.Valid && canSettings.Int64 != 0)
				user.CanManageTeams = user.IsOwner || user.IsAdmin || (canTeams.Valid && canTeams.Int64 != 0)
				if err := h.loadAuthorization(r.Context(), &user, ""); err != nil {
					h.writeError(w, 401, "invalid session")
					return
				}
				h.authorizedCall(w, r, user, next)
				return
			}
		}

		h.writeError(w, http.StatusUnauthorized, "authentication required")
	}
}

func extractAPIKey(r *http.Request) string {
	if k := r.Header.Get("X-API-Key"); k != "" {
		return k
	}
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}

func hashAPIKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}
