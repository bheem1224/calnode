package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/abuse"
	"github.com/calnode/calnode/internal/authsecurity"
)

// Existing bcrypt credentials remain usable; unknown accounts do the same
// memory-hard verification as newly configured accounts.
var dummyHash = authsecurity.DummyPasswordHash()

// LoginEmail handles POST /v1/auth/login/email — email + password authentication.
func (h *Handler) LoginEmail(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if req.Email == "" || req.Password == "" {
		h.writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}

	if !h.takeBudget(w, r, abuse.Rule{Key: "password:instance", Limit: abuse.Limit("LOGIN_ATTEMPTS_PER_MINUTE", 600), Window: time.Minute}) {
		return
	}
	if err := h.reservePasswordAttempt(r.Context(), req.Email); err != nil {
		if errors.Is(err, abuse.ErrLimited) {
			w.Header().Set("Retry-After", "60")
			h.writeError(w, 429, abuse.ErrLimited.Error())
		} else {
			h.writeError(w, 503, "temporarily unavailable")
		}
		return
	}

	var userID, storedHash string
	var emailLogin int
	var archivedAt sql.NullString
	err := h.db.QueryRowContext(r.Context(),
		`SELECT id, COALESCE(password_hash,''), email_login, archived_at FROM users WHERE email = ?`,
		req.Email).Scan(&userID, &storedHash, &emailLogin, &archivedAt)

	// Always run bcrypt to prevent user enumeration via timing side-channel.
	hashToCompare := storedHash
	if err != nil || emailLogin == 0 || storedHash == "" {
		hashToCompare = string(dummyHash)
	}
	bcryptErr := authsecurity.ComparePassword(hashToCompare, req.Password)

	if err != nil || emailLogin == 0 || storedHash == "" || bcryptErr != nil {
		h.writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}

	// Credentials are valid — but an archived member can't sign in. Tell them why
	// (only after the password check, so this never leaks archived status to a
	// wrong-password attempt).
	if archivedAt.Valid {
		h.writeError(w, http.StatusForbidden,
			"Your account has been archived. If you think this is an error, please contact your workspace admin.")
		return
	}

	if !h.passwordAllowed(r.Context(), userID) {
		h.writeError(w, 401, "invalid email or password")
		return
	}
	if _, err := h.db.ExecContext(r.Context(), `DELETE FROM login_backoff WHERE account_hash=?`, abuse.Hash(req.Email)); err != nil {
		h.writeError(w, 503, "temporarily unavailable")
		return
	}

	if err := h.createSession(r.Context(), w, userID); err != nil {
		if errors.Is(err, errNativeRecoveryRequired) {
			h.writeError(w, 403, err.Error())
			return
		}
		h.logger.ErrorContext(r.Context(), "email login: create session", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ChangePassword handles POST /v1/users/me/password — changes the authenticated
// user's own password. Requires email_login = 1 and the current password.
func (h *Handler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	if !h.passwordAllowed(r.Context(), user.ID) {
		h.writeError(w, 403, "password sign-in is disabled for this account")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if msg := h.validatePassword(r.Context(), req.NewPassword); msg != "" {
		h.writeError(w, http.StatusBadRequest, msg)
		return
	}

	var storedHash string
	var emailLogin int
	if err := h.db.QueryRowContext(r.Context(),
		`SELECT COALESCE(password_hash,''), email_login FROM users WHERE id = ?`, user.ID).
		Scan(&storedHash, &emailLogin); err != nil {
		h.logger.ErrorContext(r.Context(), "change password: db query", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if emailLogin == 0 || storedHash == "" {
		h.writeError(w, http.StatusBadRequest, "email login is not enabled for this account")
		return
	}
	if err := authsecurity.ComparePassword(storedHash, req.CurrentPassword); err != nil {
		h.writeError(w, http.StatusUnauthorized, "current password is incorrect")
		return
	}

	newHash, err := authsecurity.HashPassword(req.NewPassword)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "change password: bcrypt", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.writeError(w, 500, "could not change password")
		return
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(r.Context(), `UPDATE users SET password_hash=? WHERE id=? AND password_hash=?`, string(newHash), user.ID, storedHash)
	if err != nil {
		h.writeError(w, 500, "could not change password")
		return
	}
	if n, _ := result.RowsAffected(); n != 1 {
		h.writeError(w, 409, "password changed; sign in again")
		return
	}
	for _, table := range []string{"auth_challenges", "oauth_access_tokens", "oauth_auth_codes", "magic_link_tokens", "password_reset_tokens"} {
		if _, err = tx.ExecContext(r.Context(), `DELETE FROM `+table+` WHERE user_id=?`, user.ID); err != nil {
			h.writeError(w, 500, "could not revoke old authentication")
			return
		}
	}
	if _, err = tx.ExecContext(r.Context(), `DELETE FROM sessions WHERE (user_id=? OR impersonator_user_id=?) AND id<>?`, user.ID, user.ID, user.SessionID); err != nil {
		h.writeError(w, 500, "could not revoke sessions")
		return
	}
	if tx.Commit() != nil {
		h.writeError(w, 500, "could not change password")
		return
	}
	h.securityNotice(r.Context(), user.ID, "Your password was changed", "")

	h.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// revokeOtherSessions deletes every session row for userID except the one the current
// request is authenticated with (if any — an API-key-authenticated request has none to
// keep). Called after a password change: without this, a compromised session cookie
// survives the standard "change your password" remediation for up to the session's
// full TTL, since changing the password hash alone doesn't touch the sessions table.
func (h *Handler) revokeOtherSessions(r *http.Request, userID string) {
	var currentSessionID string
	if c, err := r.Cookie(sessionCookieName); err == nil {
		currentSessionID = c.Value
	}
	if _, err := h.db.ExecContext(r.Context(),
		`DELETE FROM sessions WHERE (user_id = ? OR impersonator_user_id=?) AND id != ?`, userID, userID, currentSessionID); err != nil {
		h.logger.ErrorContext(r.Context(), "revoke other sessions", "error", err, "user_id", userID)
	}
}

// AdminSetPassword handles POST /v1/users/{id}/password — admin resets another
// user's password without needing the current password. Admins must use
// ChangePassword to change their own password (which requires the current one).
func (h *Handler) AdminSetPassword(w http.ResponseWriter, r *http.Request) {
	admin, ok := userFromContext(r.Context())
	if !ok || !admin.IsAdmin {
		h.writeError(w, http.StatusForbidden, "admin access required")
		return
	}
	targetID := r.PathValue("id")
	if targetID == "" {
		h.writeError(w, http.StatusBadRequest, "user id is required")
		return
	}
	if targetID == admin.ID {
		h.writeError(w, http.StatusBadRequest, "use POST /v1/users/me/password to change your own password")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if msg := h.validatePassword(r.Context(), req.Password); msg != "" {
		h.writeError(w, http.StatusBadRequest, msg)
		return
	}

	// Delegated helpdesk roles recover Members only. Actual instance administrators
	// may recover peer administrators after fresh verification; the Owner is protected.
	var targetIsAdmin, targetIsOwner int
	err := h.db.QueryRowContext(r.Context(),
		`SELECT is_admin, is_owner FROM users WHERE id = ?`, targetID).
		Scan(&targetIsAdmin, &targetIsOwner)
	if errors.Is(err, sql.ErrNoRows) {
		h.writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "admin set password: db query", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !h.passwordAllowed(r.Context(), targetID) {
		h.writeError(w, 403, "password sign-in is disabled for this account")
		return
	}
	if targetIsAdmin != 0 && !admin.InstanceAdmin && !admin.InstanceOwner {
		h.writeError(w, 403, "only an instance administrator can recover another administrator")
		return
	}
	if targetIsOwner != 0 {
		h.writeError(w, http.StatusForbidden, "the owner's password can only be changed by the owner")
		return
	}

	if (targetIsAdmin != 0 || h.hasMFA(r.Context(), admin.ID) || h.mfaRequired(r.Context(), admin.ID)) && !h.freshAuthFactor(w, r, admin) {
		return
	}
	newHash, err := authsecurity.HashPassword(req.Password)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "admin set password: bcrypt", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.writeError(w, 500, "could not reset password")
		return
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(r.Context(), `UPDATE users SET password_hash=?,email_login=1 WHERE id=? AND is_owner=0 AND (?=1 OR is_admin=0)`, string(newHash), targetID, admin.InstanceAdmin || admin.InstanceOwner)
	if err != nil {
		h.writeError(w, 500, "could not reset password")
		return
	}
	if n, _ := result.RowsAffected(); n != 1 {
		h.writeError(w, 409, "target account changed; reload and try again")
		return
	}
	if _, err = tx.ExecContext(r.Context(), `DELETE FROM auth_challenges WHERE user_id=?`, targetID); err != nil {
		h.writeError(w, 500, "could not revoke pending authentication")
		return
	}
	if _, err = tx.ExecContext(r.Context(), `DELETE FROM sessions WHERE user_id=? OR impersonator_user_id=?`, targetID, targetID); err != nil {
		h.writeError(w, 500, "could not revoke sessions")
		return
	}
	if _, err = tx.ExecContext(r.Context(), `DELETE FROM oauth_access_tokens WHERE user_id=?`, targetID); err != nil {
		h.writeError(w, 500, "could not revoke agent connections")
		return
	}
	for _, table := range []string{"oauth_auth_codes", "magic_link_tokens", "password_reset_tokens"} {
		if _, err = tx.ExecContext(r.Context(), `DELETE FROM `+table+` WHERE user_id=?`, targetID); err != nil {
			h.writeError(w, 500, "could not revoke pending credentials")
			return
		}
	}
	if targetIsAdmin != 0 {
		if _, err = tx.ExecContext(r.Context(), `DELETE FROM api_keys WHERE user_id=?`, targetID); err != nil {
			h.writeError(w, 500, "could not revoke administrator keys")
			return
		}
	}
	if err = tx.Commit(); err != nil {
		h.writeError(w, 500, "could not reset password")
		return
	}
	h.securityAudit(r.Context(), admin.ID, "member.password_reset", targetID, nil)
	h.securityNotice(r.Context(), targetID, "An administrator reset your password", "")
	h.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
