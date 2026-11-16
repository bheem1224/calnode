package handler

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/uid"
)

type availScheduleJSON struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Timezone  string `json:"timezone"`
	IsDefault bool   `json:"is_default"`
}

// ListAvailabilitySchedules handles GET /v1/availability-schedules.
func (h *Handler) ListAvailabilitySchedules(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	rows, err := h.db.QueryContext(r.Context(), `
		SELECT id, name, timezone, is_default
		FROM availability_schedules
		WHERE user_id = ?
		ORDER BY is_default DESC, created_at ASC`, user.ID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list availability schedules", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	items := []availScheduleJSON{}
	for rows.Next() {
		var s availScheduleJSON
		var isDef int
		if err := rows.Scan(&s.ID, &s.Name, &s.Timezone, &isDef); err != nil {
			h.logger.ErrorContext(r.Context(), "scan availability schedule", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		s.IsDefault = isDef != 0
		items = append(items, s)
	}
	if err := rows.Err(); err != nil {
		h.logger.ErrorContext(r.Context(), "iterate availability schedules", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// CreateAvailabilitySchedule handles POST /v1/availability-schedules.
func (h *Handler) CreateAvailabilitySchedule(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)

	var req struct {
		Name      string `json:"name"`
		Timezone  string `json:"timezone"`
		IsDefault bool   `json:"is_default"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Timezone = strings.TrimSpace(req.Timezone)
	if req.Name == "" {
		h.writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Timezone != "" {
		if _, err := time.LoadLocation(req.Timezone); err != nil {
			h.writeError(w, http.StatusBadRequest, "invalid timezone: "+req.Timezone)
			return
		}
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "create schedule begin tx", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback() // #nosec G104

	if req.IsDefault {
		if _, err := tx.ExecContext(r.Context(),
			`UPDATE availability_schedules SET is_default = 0 WHERE user_id = ?`, user.ID); err != nil {
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}
	isDefInt := 0
	if req.IsDefault {
		isDefInt = 1
	}
	id := uid.New()
	if _, err := tx.ExecContext(r.Context(), `
		INSERT INTO availability_schedules (id, user_id, name, timezone, is_default)
		VALUES (?, ?, ?, ?, ?)`, id, user.ID, req.Name, req.Timezone, isDefInt); err != nil {
		h.logger.ErrorContext(r.Context(), "insert availability schedule", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := tx.Commit(); err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusCreated, availScheduleJSON{
		ID:        id,
		Name:      req.Name,
		Timezone:  req.Timezone,
		IsDefault: req.IsDefault,
	})
}

// PatchAvailabilitySchedule handles PATCH /v1/availability-schedules/{id}.
func (h *Handler) PatchAvailabilitySchedule(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	id := r.PathValue("id")
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)

	var curName, curTZ string
	var curDef int
	err := h.db.QueryRowContext(r.Context(), `
		SELECT name, timezone, is_default FROM availability_schedules WHERE id = ? AND user_id = ?`, id, user.ID).
		Scan(&curName, &curTZ, &curDef)
	if err == sql.ErrNoRows {
		h.writeError(w, http.StatusNotFound, "schedule not found")
		return
	}
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	var req struct {
		Name      *string `json:"name"`
		Timezone  *string `json:"timezone"`
		IsDefault *bool   `json:"is_default"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Name != nil {
		n := strings.TrimSpace(*req.Name)
		if n == "" {
			h.writeError(w, http.StatusBadRequest, "name cannot be empty")
			return
		}
		curName = n
	}
	if req.Timezone != nil {
		tz := strings.TrimSpace(*req.Timezone)
		if tz != "" {
			if _, err := time.LoadLocation(tz); err != nil {
				h.writeError(w, http.StatusBadRequest, "invalid timezone: "+tz)
				return
			}
		}
		curTZ = tz
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback() // #nosec G104

	if req.IsDefault != nil {
		if *req.IsDefault {
			if _, err := tx.ExecContext(r.Context(),
				`UPDATE availability_schedules SET is_default = 0 WHERE user_id = ?`, user.ID); err != nil {
				h.writeError(w, http.StatusInternalServerError, "internal error")
				return
			}
			curDef = 1
		} else {
			curDef = 0
		}
	}
	if _, err := tx.ExecContext(r.Context(), `
		UPDATE availability_schedules SET name = ?, timezone = ?, is_default = ?
		WHERE id = ? AND user_id = ?`, curName, curTZ, curDef, id, user.ID); err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := tx.Commit(); err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, availScheduleJSON{
		ID:        id,
		Name:      curName,
		Timezone:  curTZ,
		IsDefault: curDef != 0,
	})
}

// DeleteAvailabilitySchedule handles DELETE /v1/availability-schedules/{id}.
func (h *Handler) DeleteAvailabilitySchedule(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	id := r.PathValue("id")
	res, err := h.db.ExecContext(r.Context(),
		`DELETE FROM availability_schedules WHERE id = ? AND user_id = ?`, id, user.ID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		h.writeError(w, http.StatusNotFound, "schedule not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
