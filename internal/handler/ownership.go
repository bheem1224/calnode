package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/calnode/calnode/internal/mailer"
	"github.com/calnode/calnode/internal/uid"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"time"
)

// Reauthenticate grants five minutes for ownership operations. API credentials
// and impersonated sessions can never authorize these operations.
func (h *Handler) Reauthenticate(w http.ResponseWriter, r *http.Request) {
	u, _ := userFromContext(r.Context())
	if u.SessionID == "" || u.CredentialID != "" || u.ImpersonatorID != "" {
		h.writeError(w, 403, "an interactive session is required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var in struct {
		Password string `json:"password"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		h.writeError(w, 400, "invalid JSON")
		return
	}
	var hash, created string
	if h.db.QueryRowContext(r.Context(), `SELECT COALESCE(u.password_hash,''),s.created_at FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.id=? AND u.archived_at IS NULL`, u.SessionID).Scan(&hash, &created) != nil {
		h.writeError(w, 401, "session expired")
		return
	}
	valid := hash != "" && bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)) == nil
	if hash == "" {
		started, err := time.Parse(time.RFC3339Nano, created)
		valid = err == nil && time.Since(started) >= 0 && time.Since(started) < 5*time.Minute
	}
	if !valid {
		h.writeError(w, 403, "verify your password, or sign in again with your identity provider before continuing")
		return
	}
	if _, err := h.db.ExecContext(r.Context(), `UPDATE sessions SET reauthenticated_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), u.SessionID); err != nil {
		h.writeError(w, 500, "could not verify session")
		return
	}
	h.writeJSON(w, 200, map[string]bool{"ok": true})
}

func (h *Handler) freshOwnershipSession(w http.ResponseWriter, r *http.Request, u AuthUser) bool {
	if u.SessionID == "" || u.CredentialID != "" || u.ImpersonatorID != "" {
		h.writeError(w, 403, "ownership requires an interactive session")
		return false
	}
	var verified string
	if h.db.QueryRowContext(r.Context(), `SELECT COALESCE(reauthenticated_at,'') FROM sessions WHERE id=? AND user_id=?`, u.SessionID, u.ID).Scan(&verified) != nil {
		h.writeError(w, 403, "reauthentication required")
		return false
	}
	at, err := time.Parse(time.RFC3339Nano, verified)
	if err != nil || time.Since(at) < 0 || time.Since(at) > 5*time.Minute {
		h.writeError(w, 403, "reauthentication required")
		return false
	}
	return true
}

func (h *Handler) ClaimOwnership(w http.ResponseWriter, r *http.Request) {
	u, _ := userFromContext(r.Context())
	if !u.InstanceAdmin && !u.IsAdmin {
		h.writeError(w, 403, "admin access required")
		return
	}
	if !h.freshOwnershipSession(w, r, u) {
		return
	}
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.writeError(w, 500, "could not claim ownership")
		return
	}
	defer tx.Rollback() //nolint:errcheck
	result, err := tx.ExecContext(r.Context(), `UPDATE users SET is_owner=1 WHERE id=? AND is_admin=1 AND archived_at IS NULL AND NOT EXISTS(SELECT 1 FROM users WHERE is_owner=1) AND EXISTS(SELECT 1 FROM server_settings WHERE ownership_recovery_enabled=1)`, u.ID)
	if err != nil {
		h.writeError(w, 409, "ownership has already been claimed")
		return
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		h.writeError(w, 409, "ownership recovery is unavailable")
		return
	}
	if _, err = tx.ExecContext(r.Context(), `UPDATE server_settings SET ownership_recovery_enabled=0`); err == nil {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO security_audit_logs(id,actor_user_id,action,target_id) VALUES(?,?,?,?)`, uid.New(), u.ID, "ownership.claim", u.ID)
	}
	if err != nil || tx.Commit() != nil {
		h.writeError(w, 500, "could not claim ownership")
		return
	}
	h.logger.WarnContext(r.Context(), "legacy ownership claimed", "user_id", u.ID)
	h.notifyOwnershipChange(r.Context(), u.ID, u.ID, "claimed")
	h.writeJSON(w, 200, map[string]bool{"ok": true})
}

// Security notifications go to every active administrator after the committed change.
func (h *Handler) notifyOwnershipChange(ctx context.Context, actor, target, action string) {
	if !h.isEmailEnabled() {
		return
	}
	rows, err := h.db.QueryContext(ctx, `SELECT email FROM users WHERE (is_admin=1 OR is_owner=1) AND archived_at IS NULL`)
	if err != nil {
		return
	}
	recipients := []string{}
	for rows.Next() {
		var email string
		if rows.Scan(&email) == nil {
			recipients = append(recipients, email)
		}
	}
	rows.Close()
	for _, email := range recipients {
		if err = h.mailer.Send(ctx, mailer.Message{To: []string{email}, Subject: "Calnode ownership changed", Text: fmt.Sprintf("Instance ownership was %s. Actor: %s. New owner: %s.\nReview the security audit in Settings > Members & Roles: %s/admin/settings/members", action, actor, target, h.baseURL)}); err != nil {
			h.logger.ErrorContext(ctx, "ownership notification failed", "error", err)
		}
	}
}
