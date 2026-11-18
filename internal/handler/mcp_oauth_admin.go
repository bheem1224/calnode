package handler

import (
	"github.com/calnode/calnode/internal/access"
	"net/http"
)

// Admin management of MCP OAuth connections — the apps a user has connected via the
// "Connect" flow. Scoped per-user (like API keys): you see and revoke the apps you
// authorized.

type oauthConnectionJSON struct {
	Permissions access.Grants `json:"permissions"`
	Legacy      bool          `json:"legacy"`
	ID          string        `json:"id"`
	ClientName  string        `json:"client_name"`
	CreatedAt   string        `json:"created_at"`
	LastUsedAt  *string       `json:"last_used_at"`
	ExpiresAt   string        `json:"expires_at"`
}

// ListOAuthConnections handles GET /v1/oauth/connections.
func (h *Handler) ListOAuthConnections(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	target := r.URL.Query().Get("user_id")
	if target == "" {
		target = user.ID
	}
	if !h.canAccessUser(r.Context(), user, "mcp.manage", target) {
		h.writeError(w, 403, "connection access denied")
		return
	}
	rows, err := h.db.QueryContext(r.Context(), `
		SELECT t.id, COALESCE(c.client_name, ''), t.created_at, t.last_used_at, t.expires_at, COALESCE(t.permissions_json,'{}'),t.is_legacy
		FROM oauth_access_tokens t
		LEFT JOIN oauth_clients c ON c.client_id = t.client_id
		WHERE t.user_id = ?
		ORDER BY t.created_at DESC`, target)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list oauth connections", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	items := []oauthConnectionJSON{}
	for rows.Next() {
		var it oauthConnectionJSON
		var raw string
		if err := rows.Scan(&it.ID, &it.ClientName, &it.CreatedAt, &it.LastUsedAt, &it.ExpiresAt, &raw, &it.Legacy); err != nil {
			h.logger.ErrorContext(r.Context(), "scan oauth connection", "error", err)
			continue
		}
		it.Permissions = access.Parse(raw)
		if it.ClientName == "" {
			it.ClientName = "Unknown app"
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		h.logger.ErrorContext(r.Context(), "list oauth connections: rows", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// RevokeOAuthConnection handles DELETE /v1/oauth/connections/{id}: it deletes the
// access/refresh token, immediately cutting off that app's access to /mcp.
func (h *Handler) RevokeOAuthConnection(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	id := r.PathValue("id")
	var target string
	if h.db.QueryRowContext(r.Context(), `SELECT user_id FROM oauth_access_tokens WHERE id=?`, id).Scan(&target) != nil {
		h.writeError(w, 404, "connection not found")
		return
	}
	if !h.canAccessUser(r.Context(), user, "mcp.manage", target) {
		h.writeError(w, 403, "connection access denied")
		return
	}
	res, err := h.db.ExecContext(r.Context(),
		`DELETE FROM oauth_access_tokens WHERE id = ? AND user_id = ?`, id, target)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "revoke oauth connection", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.securityAudit(r.Context(), user.ID, "mcp.revoke", id, nil)
	if n, _ := res.RowsAffected(); n == 0 {
		h.writeError(w, http.StatusNotFound, "connection not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
