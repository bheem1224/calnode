package handler

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/calnode/calnode/internal/db"
	"github.com/calnode/calnode/internal/uid"
)

type roleJSON struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	IsSystem          bool   `json:"is_system"`
	CanImpersonate    bool   `json:"can_impersonate"`
	IsImpersonatable  bool   `json:"is_impersonatable"`
	CanManageUsers    bool   `json:"can_manage_users"`
	CanManageSettings bool   `json:"can_manage_settings"`
	CanManageTeams    bool   `json:"can_manage_teams"`
	CreatedAt         string `json:"created_at"`
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ListRoles handles GET /v1/roles (admin only).
func (h *Handler) ListRoles(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	rows, err := h.db.QueryContext(r.Context(), `
		SELECT id, name, is_system, allow_impersonation, is_impersonatable, can_manage_users, can_manage_settings, can_manage_teams, created_at
		FROM roles
		ORDER BY is_system DESC, name ASC`)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list roles", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	items := []roleJSON{}
	for rows.Next() {
		var item roleJSON
		var isSys, allowImp, isImpTarget, canUsers, canSettings, canTeams int
		if err := rows.Scan(&item.ID, &item.Name, &isSys, &allowImp, &isImpTarget, &canUsers, &canSettings, &canTeams, &item.CreatedAt); err != nil {
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		item.IsSystem = isSys != 0
		item.CanImpersonate = allowImp != 0
		item.IsImpersonatable = isImpTarget != 0
		item.CanManageUsers = canUsers != 0
		item.CanManageSettings = canSettings != 0
		item.CanManageTeams = canTeams != 0
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// CreateRole handles POST /v1/roles (admin/owner only).
func (h *Handler) CreateRole(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var req struct {
		Name               string `json:"name"`
		CanImpersonate     bool   `json:"can_impersonate"`
		AllowImpersonation bool   `json:"allow_impersonation"` // legacy alias
		IsImpersonatable   *bool  `json:"is_impersonatable"`
		CanManageUsers     bool   `json:"can_manage_users"`
		CanManageSettings  bool   `json:"can_manage_settings"`
		CanManageTeams     bool   `json:"can_manage_teams"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		h.writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	canImp := req.CanImpersonate || req.AllowImpersonation
	isImpTarget := true
	if req.IsImpersonatable != nil {
		isImpTarget = *req.IsImpersonatable
	}

	id := uid.New()
	if _, err := h.db.ExecContext(r.Context(), `
		INSERT INTO roles (id, name, is_system, allow_impersonation, is_impersonatable, can_manage_users, can_manage_settings, can_manage_teams)
		VALUES (?, ?, 0, ?, ?, ?, ?, ?)`,
		id, req.Name, boolInt(canImp), boolInt(isImpTarget), boolInt(req.CanManageUsers), boolInt(req.CanManageSettings), boolInt(req.CanManageTeams)); err != nil {
		if db.IsUniqueViolation(err) {
			h.writeError(w, http.StatusConflict, "role name already exists")
			return
		}
		h.logger.ErrorContext(r.Context(), "create role", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusCreated, roleJSON{
		ID:                id,
		Name:              req.Name,
		IsSystem:          false,
		CanImpersonate:    canImp,
		IsImpersonatable:  isImpTarget,
		CanManageUsers:    req.CanManageUsers,
		CanManageSettings: req.CanManageSettings,
		CanManageTeams:    req.CanManageTeams,
	})
}

// DeleteRole handles DELETE /v1/roles/{id} (admin/owner only; system roles protected).
func (h *Handler) DeleteRole(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	id := r.PathValue("id")
	var isSys int
	err := h.db.QueryRowContext(r.Context(), `SELECT is_system FROM roles WHERE id = ?`, id).Scan(&isSys)
	if err == sql.ErrNoRows {
		h.writeError(w, http.StatusNotFound, "role not found")
		return
	}
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if isSys != 0 {
		h.writeError(w, http.StatusForbidden, "cannot delete built-in system role")
		return
	}
	if _, err := h.db.ExecContext(r.Context(), `DELETE FROM roles WHERE id = ?`, id); err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// SetUserCustomRole handles PATCH /v1/users/{id}/custom-role (admin/owner only).
func (h *Handler) SetUserCustomRole(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	targetID := r.PathValue("id")
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var req struct {
		CustomRoleID string `json:"custom_role_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	roleID := strings.TrimSpace(req.CustomRoleID)

	var targetIsOwner int
	err := h.db.QueryRowContext(r.Context(), `SELECT is_owner FROM users WHERE id = ?`, targetID).Scan(&targetIsOwner)
	if err == sql.ErrNoRows {
		h.writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if targetIsOwner != 0 && !actor.IsOwner {
		h.writeError(w, http.StatusForbidden, "only the workspace owner can change the owner's custom role")
		return
	}

	var val any = nil
	if roleID != "" {
		var exists int
		if err := h.db.QueryRowContext(r.Context(), `SELECT 1 FROM roles WHERE id = ?`, roleID).Scan(&exists); err != nil {
			h.writeError(w, http.StatusBadRequest, "invalid custom_role_id")
			return
		}
		val = roleID
	}
	if _, err := h.db.ExecContext(r.Context(), `UPDATE users SET custom_role_id = ? WHERE id = ?`, val, targetID); err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"id": targetID, "custom_role_id": roleID})
}

// StartImpersonation handles POST /v1/admin/impersonate.
// Requires an active cookie session whose user holds a custom role with allow_impersonation = 1.
// Built-in admin and member roles do NOT grant impersonation by default.
func (h *Handler) StartImpersonation(w http.ResponseWriter, r *http.Request) {
	actor, _ := userFromContext(r.Context())
	if actor.SessionID == "" {
		h.writeError(w, http.StatusBadRequest, "impersonation requires a browser session")
		return
	}
	if actor.ImpersonatorID != "" {
		h.writeError(w, http.StatusBadRequest, "already impersonating a user; exit impersonation first")
		return
	}
	if !actor.CanImpersonate {
		h.writeError(w, http.StatusForbidden, "your role does not grant user impersonation")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var req struct {
		UserID string `json:"user_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	targetID := strings.TrimSpace(req.UserID)
	if targetID == "" || targetID == actor.ID {
		h.writeError(w, http.StatusBadRequest, "valid target user_id is required")
		return
	}

	var targetIsOwner, targetIsAdmin, targetRoleImpersonatable int
	var targetName, targetEmail string
	var archivedAt sql.NullString
	err := h.db.QueryRowContext(r.Context(), `
		SELECT u.name, u.email, u.is_owner, u.is_admin, u.archived_at, COALESCE(r.is_impersonatable, 1)
		FROM users u
		LEFT JOIN roles r ON r.id = u.custom_role_id
		WHERE u.id = ?`, targetID).
		Scan(&targetName, &targetEmail, &targetIsOwner, &targetIsAdmin, &archivedAt, &targetRoleImpersonatable)
	if err == sql.ErrNoRows {
		h.writeError(w, http.StatusNotFound, "target user not found")
		return
	}
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if archivedAt.Valid {
		h.writeError(w, http.StatusBadRequest, "cannot impersonate an archived user")
		return
	}
	if targetIsOwner != 0 {
		h.writeError(w, http.StatusForbidden, "cannot impersonate the workspace owner")
		return
	}
	if targetIsAdmin != 0 && !actor.IsOwner {
		h.writeError(w, http.StatusForbidden, "only the workspace owner can impersonate an admin")
		return
	}
	if targetRoleImpersonatable == 0 {
		h.writeError(w, http.StatusForbidden, "this user's role does not allow impersonation")
		return
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback() // #nosec G104

	if _, err := tx.ExecContext(r.Context(), `
		UPDATE sessions SET user_id = ?, impersonator_user_id = ? WHERE id = ?`,
		targetID, actor.ID, actor.SessionID); err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if _, err := tx.ExecContext(r.Context(), `
		INSERT INTO impersonation_audit_logs (id, actor_user_id, target_user_id, action)
		VALUES (?, ?, ?, 'start')`, uid.New(), actor.ID, targetID); err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := tx.Commit(); err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"impersonating": true,
		"user_id":       targetID,
		"name":          targetName,
		"email":         targetEmail,
	})
}

// StopImpersonation handles POST /v1/admin/impersonate/stop.
func (h *Handler) StopImpersonation(w http.ResponseWriter, r *http.Request) {
	current, _ := userFromContext(r.Context())
	if current.SessionID == "" || current.ImpersonatorID == "" {
		h.writeError(w, http.StatusBadRequest, "not currently impersonating")
		return
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback() // #nosec G104

	if _, err := tx.ExecContext(r.Context(), `
		UPDATE sessions SET user_id = ?, impersonator_user_id = NULL WHERE id = ?`,
		current.ImpersonatorID, current.SessionID); err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if _, err := tx.ExecContext(r.Context(), `
		INSERT INTO impersonation_audit_logs (id, actor_user_id, target_user_id, action)
		VALUES (?, ?, ?, 'stop')`, uid.New(), current.ImpersonatorID, current.ID); err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := tx.Commit(); err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"impersonating": false})
}

// ListImpersonationLogs handles GET /v1/admin/impersonate/logs (admin only).
func (h *Handler) ListImpersonationLogs(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	rows, err := h.db.QueryContext(r.Context(), `
		SELECT l.id, l.actor_user_id, COALESCE(a.name,''), COALESCE(a.email,''),
		       l.target_user_id, COALESCE(t.name,''), COALESCE(t.email,''),
		       l.action, l.created_at
		FROM impersonation_audit_logs l
		LEFT JOIN users a ON a.id = l.actor_user_id
		LEFT JOIN users t ON t.id = l.target_user_id
		ORDER BY l.created_at DESC
		LIMIT 100`)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	type logEntry struct {
		ID           string `json:"id"`
		ActorUserID  string `json:"actor_user_id"`
		ActorName    string `json:"actor_name"`
		ActorEmail   string `json:"actor_email"`
		TargetUserID string `json:"target_user_id"`
		TargetName   string `json:"target_name"`
		TargetEmail  string `json:"target_email"`
		Action       string `json:"action"`
		CreatedAt    string `json:"created_at"`
	}
	items := []logEntry{}
	for rows.Next() {
		var e logEntry
		if err := rows.Scan(&e.ID, &e.ActorUserID, &e.ActorName, &e.ActorEmail,
			&e.TargetUserID, &e.TargetName, &e.TargetEmail, &e.Action, &e.CreatedAt); err != nil {
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		items = append(items, e)
	}
	if err := rows.Err(); err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
