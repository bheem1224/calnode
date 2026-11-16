package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/uid"
)

type oooEntryJSON struct {
	ID                 string   `json:"id"`
	UserID             string   `json:"user_id"`
	TeamID             *string  `json:"team_id,omitempty"`
	TeamName           string   `json:"team_name,omitempty"`
	Dates              []string `json:"dates"`
	Reason             string   `json:"reason"`
	PublicNote         string   `json:"public_note"`
	RedirectUserID     *string  `json:"redirect_user_id,omitempty"`
	RedirectUserName   string   `json:"redirect_user_name,omitempty"`
	RedirectUserHandle string   `json:"redirect_user_handle,omitempty"`
	CreatedAt          string   `json:"created_at"`
}

func validOOOReason(r string) bool {
	switch r {
	case "ooo", "vacation", "holiday", "sick", "travel", "other":
		return true
	}
	return false
}

func normalizeOOODates(in []string) ([]string, bool) {
	seen := make(map[string]bool, len(in))
	var out []string
	for _, raw := range in {
		d := strings.TrimSpace(raw)
		if d == "" {
			continue
		}
		if _, err := time.Parse("2006-01-02", d); err != nil {
			return nil, false
		}
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out, len(out) > 0
}

// ListOOOEntries handles GET /v1/ooo.
func (h *Handler) ListOOOEntries(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	rows, err := h.db.QueryContext(r.Context(), `
		SELECT o.id, o.user_id, o.team_id, COALESCE(t.name, ''), o.dates_json,
		       o.reason, o.public_note, o.redirect_user_id,
		       COALESCE(ru.name, ''), COALESCE(ru.handle, ''), o.created_at
		FROM ooo_entries o
		LEFT JOIN teams t ON t.id = o.team_id
		LEFT JOIN users ru ON ru.id = o.redirect_user_id
		WHERE o.user_id = ?
		   OR (o.team_id IS NOT NULL AND o.team_id IN (SELECT team_id FROM team_members WHERE user_id = ?))
		ORDER BY o.created_at DESC`, user.ID, user.ID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list ooo entries", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	items := []oooEntryJSON{}
	for rows.Next() {
		var e oooEntryJSON
		var teamID, redirID sql.NullString
		var datesRaw string
		if err := rows.Scan(&e.ID, &e.UserID, &teamID, &e.TeamName, &datesRaw,
			&e.Reason, &e.PublicNote, &redirID, &e.RedirectUserName, &e.RedirectUserHandle, &e.CreatedAt); err != nil {
			h.logger.ErrorContext(r.Context(), "scan ooo entry", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if teamID.Valid {
			e.TeamID = &teamID.String
		}
		if redirID.Valid {
			e.RedirectUserID = &redirID.String
		}
		_ = json.Unmarshal([]byte(datesRaw), &e.Dates)
		if e.Dates == nil {
			e.Dates = []string{}
		}
		items = append(items, e)
	}
	if err := rows.Err(); err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// CreateOOOEntry handles POST /v1/ooo.
func (h *Handler) CreateOOOEntry(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)

	var req struct {
		TeamID         *string  `json:"team_id"`
		Dates          []string `json:"dates"`
		Reason         string   `json:"reason"`
		PublicNote     string   `json:"public_note"`
		RedirectUserID *string  `json:"redirect_user_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	dates, ok := normalizeOOODates(req.Dates)
	if !ok {
		h.writeError(w, http.StatusBadRequest, "dates must be a non-empty list of YYYY-MM-DD dates")
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		reason = "ooo"
	}
	if !validOOOReason(reason) {
		h.writeError(w, http.StatusBadRequest, "invalid reason")
		return
	}
	req.PublicNote = strings.TrimSpace(req.PublicNote)

	var teamID *string
	if req.TeamID != nil && strings.TrimSpace(*req.TeamID) != "" {
		tid := strings.TrimSpace(*req.TeamID)
		if !user.IsAdmin && !user.IsOwner {
			var member int
			if err := h.db.QueryRowContext(r.Context(),
				`SELECT 1 FROM team_members WHERE team_id = ? AND user_id = ?`, tid, user.ID).Scan(&member); err != nil {
				h.writeError(w, http.StatusForbidden, "not a member of this team")
				return
			}
		}
		teamID = &tid
	}

	var redirID *string
	if req.RedirectUserID != nil && strings.TrimSpace(*req.RedirectUserID) != "" {
		rid := strings.TrimSpace(*req.RedirectUserID)
		if rid == user.ID {
			h.writeError(w, http.StatusBadRequest, "cannot redirect OOO to yourself")
			return
		}
		var exists int
		if err := h.db.QueryRowContext(r.Context(),
			`SELECT 1 FROM users WHERE id = ? AND archived_at IS NULL`, rid).Scan(&exists); err != nil {
			h.writeError(w, http.StatusBadRequest, "invalid redirect_user_id")
			return
		}
		redirID = &rid
	}

	datesBytes, _ := json.Marshal(dates)
	id := uid.New()
	if _, err := h.db.ExecContext(r.Context(), `
		INSERT INTO ooo_entries (id, user_id, team_id, dates_json, reason, public_note, redirect_user_id)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, user.ID, teamID, string(datesBytes), reason, req.PublicNote, redirID); err != nil {
		h.logger.ErrorContext(r.Context(), "create ooo entry", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.writeJSON(w, http.StatusCreated, oooEntryJSON{
		ID:             id,
		UserID:         user.ID,
		TeamID:         teamID,
		Dates:          dates,
		Reason:         reason,
		PublicNote:     req.PublicNote,
		RedirectUserID: redirID,
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
	})
}

// DeleteOOOEntry handles DELETE /v1/ooo/{id}.
func (h *Handler) DeleteOOOEntry(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	id := r.PathValue("id")

	var res sql.Result
	var err error
	if user.IsAdmin || user.IsOwner {
		res, err = h.db.ExecContext(r.Context(), `DELETE FROM ooo_entries WHERE id = ?`, id)
	} else {
		res, err = h.db.ExecContext(r.Context(), `DELETE FROM ooo_entries WHERE id = ? AND user_id = ?`, id, user.ID)
	}
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		h.writeError(w, http.StatusNotFound, "OOO entry not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) loadUserOOODates(ctx context.Context, userID string) ([]time.Time, error) {
	rows, err := h.db.QueryContext(ctx, `
		SELECT dates_json
		FROM ooo_entries
		WHERE user_id = ?
		   OR (team_id IS NOT NULL AND team_id IN (SELECT team_id FROM team_members WHERE user_id = ?))`, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []time.Time
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var dates []string
		if err := json.Unmarshal([]byte(raw), &dates); err != nil {
			continue
		}
		for _, d := range dates {
			if parsed, err := time.Parse("2006-01-02", d); err == nil {
				out = append(out, parsed)
			}
		}
	}
	return out, rows.Err()
}
