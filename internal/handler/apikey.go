package handler

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"github.com/calnode/calnode/internal/access"
	"net/http"
	"time"

	"github.com/calnode/calnode/internal/uid"
)

// ListAPIKeys handles GET /v1/api-keys.
func (h *Handler) ListAPIKeys(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	target := r.URL.Query().Get("user_id")
	if target == "" {
		target = user.ID
	}
	if target != user.ID && !h.canAccessUser(r.Context(), user, "api_keys.manage", target) {
		h.writeError(w, 403, "key access denied")
		return
	}
	rows, err := h.db.QueryContext(r.Context(), `
		SELECT id, name, created_at, last_used_at, permissions_json, expires_at, is_legacy
		FROM api_keys WHERE user_id = ?
		ORDER BY created_at DESC`, target)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list api keys", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	type keyItem struct {
		ID          string        `json:"id"`
		Name        string        `json:"name"`
		CreatedAt   string        `json:"created_at"`
		LastUsedAt  *string       `json:"last_used_at"`
		Permissions access.Grants `json:"permissions"`
		ExpiresAt   *string       `json:"expires_at"`
		Legacy      bool          `json:"legacy"`
	}
	items := []keyItem{}
	for rows.Next() {
		var item keyItem
		var grants *string
		if err := rows.Scan(&item.ID, &item.Name, &item.CreatedAt, &item.LastUsedAt, &grants, &item.ExpiresAt, &item.Legacy); err != nil {
			h.logger.ErrorContext(r.Context(), "scan api key row", "error", err)
			continue
		}
		if grants != nil {
			item.Permissions = access.Parse(*grants)
		} else {
			item.Legacy = true
			item.Permissions = access.Grants{}
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		h.logger.ErrorContext(r.Context(), "list api keys: rows", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// CreateAPIKey handles POST /v1/api-keys.
// The plaintext key is returned once and must be saved by the caller.
func (h *Handler) CreateAPIKey(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())

	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var req struct {
		Name        string         `json:"name"`
		Permissions *access.Grants `json:"permissions"`
		ExpiresAt   string         `json:"expires_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Name == "" {
		h.writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if len(req.Name) > 255 {
		h.writeError(w, http.StatusBadRequest, "name must be 255 characters or fewer")
		return
	}
	if req.Permissions == nil || len(*req.Permissions) == 0 {
		h.writeError(w, 400, "select explicit permissions for this key")
		return
	}
	if err := access.Validate(*req.Permissions, user.Permissions, true); err != nil {
		h.writeError(w, 403, err.Error())
		return
	}
	expires := time.Now().UTC().Add(90 * 24 * time.Hour)
	if req.ExpiresAt != "" {
		var err error
		expires, err = time.Parse(time.RFC3339, req.ExpiresAt)
		if err != nil || !expires.After(time.Now()) || expires.After(time.Now().Add(365*24*time.Hour)) {
			h.writeError(w, 400, "expiry must be within the next 365 days")
			return
		}
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		h.logger.ErrorContext(r.Context(), "create api key: rand", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	plainKey := "cno_" + hex.EncodeToString(raw)
	keyHash := hashAPIKey(plainKey)

	keyID := uid.New()
	now := time.Now().UTC().Format(time.RFC3339Nano)

	if _, err := h.db.ExecContext(r.Context(), `
		INSERT INTO api_keys (id, user_id, name, key_hash, created_at,permissions_json,expires_at)
		VALUES (?, ?, ?, ?, ?,?,?)`,
		keyID, user.ID, req.Name, keyHash, now, req.Permissions.JSON(), expires.Format(time.RFC3339)); err != nil {
		h.logger.ErrorContext(r.Context(), "create api key: insert", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.securityAudit(r.Context(), user.ID, "api_key.create", keyID, *req.Permissions)

	h.writeJSON(w, http.StatusCreated, map[string]any{
		"id":          keyID,
		"name":        req.Name,
		"key":         plainKey,
		"created_at":  now,
		"permissions": *req.Permissions,
		"expires_at":  expires.Format(time.RFC3339),
		"note":        "save this key — it will not be shown again",
	})
}

// DeleteAPIKey handles DELETE /v1/api-keys/{id}.
func (h *Handler) DeleteAPIKey(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	id := r.PathValue("id")
	var owner string
	if h.db.QueryRowContext(r.Context(), `SELECT user_id FROM api_keys WHERE id=?`, id).Scan(&owner) != nil {
		h.writeError(w, 404, "api key not found")
		return
	}
	if owner != user.ID && (user.CredentialID != "" || !user.Permissions.Has("api_keys.manage") || !h.canAccessUser(r.Context(), user, "api_keys.manage", owner)) {
		h.writeError(w, 403, "permission required: api_keys.manage")
		return
	}

	res, err := h.db.ExecContext(r.Context(), `
		DELETE FROM api_keys WHERE id = ? AND user_id = ?`, id, owner)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "delete api key", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		h.writeError(w, http.StatusNotFound, "api key not found")
		return
	}
	h.securityAudit(r.Context(), user.ID, "api_key.revoke", id, nil)
	w.WriteHeader(http.StatusNoContent)
}

// UpdateAPIKey replaces a key's grant after an explicit interactive review.
// Editing a legacy key removes its legacy status; secrets are never returned.
func (h *Handler) UpdateAPIKey(w http.ResponseWriter, r *http.Request) {
	u, _ := userFromContext(r.Context())
	id := r.PathValue("id")
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var in struct {
		Permissions access.Grants `json:"permissions"`
		ExpiresAt   string        `json:"expires_at"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || len(in.Permissions) == 0 {
		h.writeError(w, 400, "explicit permissions required")
		return
	}
	if err := access.Validate(in.Permissions, u.Permissions, true); err != nil {
		h.writeError(w, 403, err.Error())
		return
	}
	expires, err := time.Parse(time.RFC3339, in.ExpiresAt)
	if err != nil || !expires.After(time.Now()) || expires.After(time.Now().Add(365*24*time.Hour)) {
		h.writeError(w, 400, "expiry must be within the next 365 days")
		return
	}
	result, err := h.db.ExecContext(r.Context(), `UPDATE api_keys SET permissions_json=?,expires_at=?,is_legacy=0 WHERE id=? AND user_id=?`, in.Permissions.JSON(), expires.Format(time.RFC3339), id, u.ID)
	if err != nil {
		h.writeError(w, 500, "could not update key")
		return
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		h.writeError(w, 404, "key not found")
		return
	}
	h.securityAudit(r.Context(), u.ID, "api_key.review", id, in.Permissions)
	h.writeJSON(w, 200, map[string]bool{"ok": true})
}
