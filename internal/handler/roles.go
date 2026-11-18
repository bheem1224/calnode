package handler

import (
	"database/sql"
	"encoding/json"
	"net/http"
)

// SetUserRole handles PATCH /v1/users/{id}/role — administrators.
//
// Body: {"role": "admin" | "member"}. Promotes or demotes a user between the
// member and admin tiers. Both owners and administrators may grant or revoke
// admin. The owner's own role cannot be changed here (use transfer-ownership),
// and you cannot change your own role.
func (h *Handler) SetUserRole(w http.ResponseWriter, r *http.Request) {
	actor, ok := userFromContext(r.Context())
	if !ok || !actor.IsAdmin {
		h.writeError(w, http.StatusForbidden, "admin access required")
		return
	}
	targetID := r.PathValue("id")
	if targetID == actor.ID {
		h.writeError(w, http.StatusBadRequest, "you cannot change your own role")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
	var req struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Role != "admin" && req.Role != "member" {
		h.writeError(w, http.StatusBadRequest, "role must be 'admin' or 'member'")
		return
	}

	var targetIsOwner, targetIsAdmin int
	err := h.db.QueryRowContext(r.Context(),
		`SELECT is_owner,is_admin FROM users WHERE id = ?`, targetID).Scan(&targetIsOwner, &targetIsAdmin)
	if err == sql.ErrNoRows {
		h.writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "set role: load target", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if targetIsOwner != 0 {
		h.writeError(w, http.StatusBadRequest, "cannot change the owner's role; transfer ownership first")
		return
	}

	if actor.Permissions != nil && (req.Role == "admin" || targetIsAdmin != 0) && !actor.Permissions.Has("members.invite_admin") {
		h.writeError(w, 403, "permission required: members.invite_admin")
		return
	}
	isAdmin := 0
	if req.Role == "admin" {
		isAdmin = 1
	}
	result, err := h.db.ExecContext(r.Context(),
		`UPDATE users SET is_admin = ?, custom_role_id=NULL, legacy_permissions=0 WHERE id = ? AND is_owner=0`, isAdmin, targetID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "set role: update", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if n, _ := result.RowsAffected(); n != 1 {
		h.writeError(w, 409, "member role changed; reload and try again")
		return
	}
	if isAdmin == 0 {
		_, _ = h.db.ExecContext(r.Context(), `DELETE FROM sessions WHERE user_id=?`, targetID)
	}
	h.securityAudit(r.Context(), actor.ID, "member.role", targetID, map[string]string{"role": req.Role})
	h.writeJSON(w, http.StatusOK, map[string]any{"id": targetID, "role": req.Role})
}

// TransferOwnership handles POST /v1/users/{id}/transfer-ownership — owner only.
//
// Moves the single owner tier to the target user (who also becomes admin), and
// demotes the current owner to admin. Exactly one owner exists at all times;
// the swap runs in one transaction so that invariant never breaks.
func (h *Handler) TransferOwnership(w http.ResponseWriter, r *http.Request) {
	actor, ok := userFromContext(r.Context())
	if !ok || !actor.IsOwner {
		h.writeError(w, http.StatusForbidden, "only the workspace owner can transfer ownership")
		return
	}
	if !h.freshOwnershipSession(w, r, actor) {
		return
	}
	targetID := r.PathValue("id")
	if targetID == actor.ID {
		h.writeError(w, http.StatusBadRequest, "you are already the owner")
		return
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "transfer ownership: begin tx", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback() //nolint:errcheck

	var exists int
	if err := tx.QueryRowContext(r.Context(),
		`SELECT 1 FROM users WHERE id = ? AND is_admin=1 AND archived_at IS NULL`, targetID).Scan(&exists); err == sql.ErrNoRows {
		h.writeError(w, http.StatusNotFound, "user not found")
		return
	} else if err != nil {
		h.logger.ErrorContext(r.Context(), "transfer ownership: load target", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Demote the current owner to admin, promote the target to owner+admin.
	result, err := tx.ExecContext(r.Context(),
		`UPDATE users SET is_owner = 0 WHERE id = ? AND is_owner=1`, actor.ID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "transfer ownership: demote", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if n, _ := result.RowsAffected(); n != 1 {
		h.writeError(w, 409, "ownership has changed; reload and try again")
		return
	}
	if _, err := tx.ExecContext(r.Context(),
		`UPDATE users SET is_owner = 1, is_admin = 1 WHERE id = ?`, targetID); err != nil {
		h.logger.ErrorContext(r.Context(), "transfer ownership: promote", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := tx.Commit(); err != nil {
		h.logger.ErrorContext(r.Context(), "transfer ownership: commit", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.securityAudit(r.Context(), actor.ID, "ownership.transfer", targetID, nil)
	h.notifyOwnershipChange(r.Context(), actor.ID, targetID, "transferred")
	h.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "new_owner_id": targetID})
}
