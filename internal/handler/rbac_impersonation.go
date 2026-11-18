package handler

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/calnode/calnode/internal/access"
	"github.com/calnode/calnode/internal/db"
	"github.com/calnode/calnode/internal/uid"
)

type roleJSON struct {
	Permissions       access.Grants `json:"permissions"`
	ID                string        `json:"id"`
	Name              string        `json:"name"`
	Description       string        `json:"description"`
	IsSystem          bool          `json:"is_system"`
	CanImpersonate    bool          `json:"can_impersonate"`
	IsImpersonatable  bool          `json:"is_impersonatable"`
	CanManageUsers    bool          `json:"can_manage_users"`
	CanManageSettings bool          `json:"can_manage_settings"`
	CanManageTeams    bool          `json:"can_manage_teams"`
	CreatedAt         string        `json:"created_at"`
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
		SELECT id, name, description, is_system, allow_impersonation, is_impersonatable, can_manage_users, can_manage_settings, can_manage_teams, created_at, permissions_json
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
		var grants sql.NullString
		var isSys, allowImp, isImpTarget, canUsers, canSettings, canTeams int
		if err := rows.Scan(&item.ID, &item.Name, &item.Description, &isSys, &allowImp, &isImpTarget, &canUsers, &canSettings, &canTeams, &item.CreatedAt, &grants); err != nil {
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		item.IsSystem = isSys != 0
		item.CanImpersonate = allowImp != 0
		item.IsImpersonatable = isImpTarget != 0
		item.CanManageUsers = canUsers != 0
		item.CanManageSettings = canSettings != 0
		item.CanManageTeams = canTeams != 0
		if grants.Valid {
			item.Permissions = access.Parse(grants.String)
		} else {
			item.Permissions = legacyRoleGrants(item)
		}
		if item.IsSystem {
			if item.ID == "role_admin" {
				item.Permissions = access.Full()
			} else {
				item.Permissions = access.Member(false)
			}
		}
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
	actor, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var req struct {
		Permissions        *access.Grants `json:"permissions"`
		Name               string         `json:"name"`
		Description        string         `json:"description"`
		CanImpersonate     bool           `json:"can_impersonate"`
		AllowImpersonation bool           `json:"allow_impersonation"` // legacy alias
		IsImpersonatable   *bool          `json:"is_impersonatable"`
		CanManageUsers     bool           `json:"can_manage_users"`
		CanManageSettings  bool           `json:"can_manage_settings"`
		CanManageTeams     bool           `json:"can_manage_teams"`
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
	var effective access.Grants
	if req.Permissions != nil {
		effective = *req.Permissions
	} else {
		effective = legacyRoleGrants(roleJSON{CanManageUsers: req.CanManageUsers, CanManageSettings: req.CanManageSettings, CanManageTeams: req.CanManageTeams, CanImpersonate: req.CanImpersonate})
	}
	if actor.Permissions != nil {
		if err := access.Validate(effective, actor.Permissions, false); err != nil {
			h.writeError(w, 403, err.Error())
			return
		}
	}
	if _, err := h.db.ExecContext(r.Context(), `
		INSERT INTO roles (id, name, description, is_system, allow_impersonation, is_impersonatable, can_manage_users, can_manage_settings, can_manage_teams,permissions_json)
		VALUES (?, ?, ?, 0, ?, ?, ?, ?, ?,?)`,
		id, req.Name, strings.TrimSpace(req.Description), boolInt(canImp), boolInt(isImpTarget), boolInt(req.CanManageUsers), boolInt(req.CanManageSettings), boolInt(req.CanManageTeams), effective.JSON()); err != nil {
		if db.IsUniqueViolation(err) {
			h.writeError(w, http.StatusConflict, "role name already exists")
			return
		}
		h.logger.ErrorContext(r.Context(), "create role", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.securityAudit(r.Context(), actor.ID, "role.save", id, effective)
	h.writeJSON(w, http.StatusCreated, roleJSON{
		Permissions: effective, ID: id,
		Name:              req.Name,
		Description:       strings.TrimSpace(req.Description),
		IsSystem:          false,
		CanImpersonate:    canImp,
		IsImpersonatable:  isImpTarget,
		CanManageUsers:    req.CanManageUsers,
		CanManageSettings: req.CanManageSettings,
		CanManageTeams:    req.CanManageTeams,
	})
}

// UpdateRole handles PATCH /v1/roles/{id} (admin/owner only; system roles protected).
func (h *Handler) UpdateRole(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireAdmin(w, r)
	if !ok {
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
		h.writeError(w, http.StatusForbidden, "cannot edit built-in system role")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var req struct {
		Permissions       *access.Grants `json:"permissions"`
		Name              string         `json:"name"`
		Description       *string        `json:"description"`
		CanImpersonate    bool           `json:"can_impersonate"`
		IsImpersonatable  bool           `json:"is_impersonatable"`
		CanManageUsers    bool           `json:"can_manage_users"`
		CanManageSettings bool           `json:"can_manage_settings"`
		CanManageTeams    bool           `json:"can_manage_teams"`
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

	var effective access.Grants
	if req.Permissions != nil {
		effective = *req.Permissions
	} else {
		effective = legacyRoleGrants(roleJSON{CanManageUsers: req.CanManageUsers, CanManageSettings: req.CanManageSettings, CanManageTeams: req.CanManageTeams, CanImpersonate: req.CanImpersonate})
	}
	if actor.Permissions != nil {
		if err := access.Validate(effective, actor.Permissions, false); err != nil {
			h.writeError(w, 403, err.Error())
			return
		}
	}
	if _, err := h.db.ExecContext(r.Context(), `
		UPDATE roles
		SET name = ?, description = COALESCE(?, description), allow_impersonation = ?, is_impersonatable = ?,
		    can_manage_users = ?, can_manage_settings = ?, can_manage_teams = ?,permissions_json=?
		WHERE id = ?`,
		req.Name, req.Description, boolInt(req.CanImpersonate), boolInt(req.IsImpersonatable),
		boolInt(req.CanManageUsers), boolInt(req.CanManageSettings), boolInt(req.CanManageTeams), effective.JSON(), id); err != nil {
		if db.IsUniqueViolation(err) {
			h.writeError(w, http.StatusConflict, "role name already exists")
			return
		}
		h.logger.ErrorContext(r.Context(), "update role", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	var description string
	if err := h.db.QueryRowContext(r.Context(), `SELECT description FROM roles WHERE id = ?`, id).Scan(&description); err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.securityAudit(r.Context(), actor.ID, "role.save", id, effective)
	h.writeJSON(w, http.StatusOK, roleJSON{
		Permissions: effective, Description: description,
		ID:                id,
		Name:              req.Name,
		IsSystem:          false,
		CanImpersonate:    req.CanImpersonate,
		IsImpersonatable:  req.IsImpersonatable,
		CanManageUsers:    req.CanManageUsers,
		CanManageSettings: req.CanManageSettings,
		CanManageTeams:    req.CanManageTeams,
	})
}

// DeleteRole handles DELETE /v1/roles/{id} (admin/owner only; system roles protected).
func (h *Handler) DeleteRole(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireAdmin(w, r)
	if !ok {
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
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.writeError(w, 500, "could not delete role")
		return
	}
	defer tx.Rollback()
	var assigned int
	if err = tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM users WHERE custom_role_id=?`, id).Scan(&assigned); err != nil {
		h.writeError(w, 500, "could not check role")
		return
	}
	if assigned > 0 {
		h.writeError(w, 409, "reassign members before deleting this role")
		return
	}
	if _, err = tx.ExecContext(r.Context(), `DELETE FROM invite_tokens WHERE custom_role_id=? AND used_at IS NULL`, id); err != nil {
		h.writeError(w, 500, "could not revoke role invitations")
		return
	}
	if _, err = tx.ExecContext(r.Context(), `DELETE FROM roles WHERE id=? AND is_system=0`, id); err != nil {
		h.writeError(w, 500, "could not delete role")
		return
	}
	if err = tx.Commit(); err != nil {
		h.writeError(w, 500, "could not delete role")
		return
	}
	h.securityAudit(r.Context(), actor.ID, "role.delete", id, nil)
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
	if targetIsOwner != 0 || targetID == actor.ID {
		h.writeError(w, http.StatusForbidden, "cannot change your own role or the owner's role")
		return
	}

	var val any = nil
	if roleID != "" {
		var exists int
		if err := h.db.QueryRowContext(r.Context(), `SELECT 1 FROM roles WHERE id = ? AND is_system=0`, roleID).Scan(&exists); err != nil {
			h.writeError(w, http.StatusBadRequest, "invalid custom_role_id")
			return
		}
		grants, err := access.Role(r.Context(), h.db, roleID)
		if err != nil {
			h.writeError(w, 400, "invalid role")
			return
		}
		if actor.Permissions != nil {
			if err := access.Validate(grants, actor.Permissions, false); err != nil {
				h.writeError(w, 403, err.Error())
				return
			}
		}
		val = roleID
	}
	if _, err := h.db.ExecContext(r.Context(), `UPDATE users SET custom_role_id = ?, legacy_permissions=0 WHERE id = ? AND is_owner=0`, val, targetID); err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.securityAudit(r.Context(), actor.ID, "member.custom_role", targetID, map[string]string{"role_id": roleID})
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

	// Recheck after acquiring the transaction, so a concurrent transfer cannot turn the target into an owner between the guard and session mutation.
	var eligible int
	if err := tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM users u LEFT JOIN roles r ON r.id=u.custom_role_id WHERE u.id=? AND u.is_owner=0 AND u.archived_at IS NULL AND COALESCE(r.is_impersonatable,1)=1`, targetID).Scan(&eligible); err != nil || eligible != 1 {
		h.writeError(w, 409, "target account changed; reload and try again")
		return
	}
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

func legacyRoleGrants(r roleJSON) access.Grants {
	return access.LegacyRole(r.CanManageUsers, r.CanManageSettings, r.CanManageTeams, r.CanImpersonate)
}
