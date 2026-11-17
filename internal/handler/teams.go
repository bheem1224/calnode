package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/db"
	"github.com/calnode/calnode/internal/uid"
)

// slugify normalises a string into a URL-safe slug: lowercase, alphanumeric
// runs joined by single hyphens, no leading/trailing hyphens.
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastHyphen := false
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastHyphen = false
		case r == ' ' || r == '-' || r == '_':
			if b.Len() > 0 && !lastHyphen {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

type teamMemberJSON struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Email           string `json:"email"`
	AvatarURL       string `json:"avatar_url,omitempty"`
	Role            string `json:"role"`
	RoutingPriority int    `json:"routing_priority"`
	Archived        bool   `json:"archived"`
}

type teamJSON struct {
	ID                  string           `json:"id"`
	Name                string           `json:"name"`
	Slug                string           `json:"slug"`
	Description         string           `json:"description,omitempty"`
	LogoURL             string           `json:"logo_url,omitempty"`
	Location            string           `json:"location,omitempty"`
	BrandColor          string           `json:"brand_color,omitempty"`
	ShowMembersPublicly bool             `json:"show_members_publicly"`
	CreatedAt           string           `json:"created_at"`
	MemberCount         int              `json:"member_count"`
	MyRole              string           `json:"my_role,omitempty"`
	Members             []teamMemberJSON `json:"members,omitempty"`
}

func (h *Handler) userTeamRole(ctx context.Context, userID, teamID string) (string, error) {
	var role string
	err := h.db.QueryRowContext(ctx,
		`SELECT role FROM team_members WHERE team_id = ? AND user_id = ?`,
		teamID, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return role, err
}

func (h *Handler) canManageTeam(ctx context.Context, user AuthUser, teamID string) (bool, error) {
	if user.IsAdmin {
		return true, nil
	}
	role, err := h.userTeamRole(ctx, user.ID, teamID)
	if err != nil {
		return false, err
	}
	return role == "owner", nil
}

// CreateTeam handles POST /v1/teams (admin). Body: {name, slug?}.
// slug is derived from the name when omitted; both are normalised.
func (h *Handler) CreateTeam(w http.ResponseWriter, r *http.Request) {
	admin, ok := userFromContext(r.Context())
	if !ok || !admin.IsAdmin {
		h.writeError(w, http.StatusForbidden, "admin access required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var req struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
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
	slug := slugify(req.Slug)
	if slug == "" {
		slug = slugify(req.Name)
	}
	if slug == "" {
		h.writeError(w, http.StatusBadRequest, "could not derive a slug from the name; provide a slug")
		return
	}

	id := uid.New()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := h.db.ExecContext(r.Context(),
		`INSERT INTO teams (id, name, slug, created_at) VALUES (?, ?, ?, ?)`,
		id, req.Name, slug, now); err != nil {
		if db.IsUniqueViolation(err) {
			h.writeError(w, http.StatusConflict, "a team with that slug already exists")
			return
		}
		h.logger.ErrorContext(r.Context(), "create team", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusCreated, teamJSON{ID: id, Name: req.Name, Slug: slug, CreatedAt: now, MemberCount: 0})
}

// ListTeams handles GET /v1/teams — returns all teams for admins, or teams the caller belongs to.
func (h *Handler) ListTeams(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var rows *sql.Rows
	var err error
	if user.IsAdmin {
		rows, err = h.db.QueryContext(r.Context(), `
			SELECT t.id, t.name, t.slug, t.description, t.logo_url, t.location, t.brand_color, t.show_members_publicly, t.created_at, COUNT(tm.user_id),
			       COALESCE((SELECT role FROM team_members WHERE team_id = t.id AND user_id = ?), '')
			FROM teams t LEFT JOIN team_members tm ON tm.team_id = t.id
			GROUP BY t.id, t.name, t.slug, t.description, t.logo_url, t.location, t.brand_color, t.show_members_publicly, t.created_at
			ORDER BY t.name ASC`, user.ID)
	} else {
		rows, err = h.db.QueryContext(r.Context(), `
			SELECT t.id, t.name, t.slug, t.description, t.logo_url, t.location, t.brand_color, t.show_members_publicly, t.created_at, COUNT(tm.user_id), my.role
			FROM teams t
			JOIN team_members my ON my.team_id = t.id AND my.user_id = ?
			LEFT JOIN team_members tm ON tm.team_id = t.id
			GROUP BY t.id, t.name, t.slug, t.description, t.logo_url, t.location, t.brand_color, t.show_members_publicly, t.created_at, my.role
			ORDER BY t.name ASC`, user.ID)
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list teams", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	out := []teamJSON{}
	for rows.Next() {
		var t teamJSON
		var showPub int
		if err := rows.Scan(&t.ID, &t.Name, &t.Slug, &t.Description, &t.LogoURL, &t.Location, &t.BrandColor, &showPub, &t.CreatedAt, &t.MemberCount, &t.MyRole); err != nil {
			continue
		}
		t.ShowMembersPublicly = showPub != 0
		out = append(out, t)
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

// GetTeam handles GET /v1/teams/{id} (admin or team member) — team + its members.
func (h *Handler) GetTeam(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id := r.PathValue("id")
	myRole, err := h.userTeamRole(r.Context(), user.ID, id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "get team: role lookup", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !user.IsAdmin && myRole == "" {
		h.writeError(w, http.StatusForbidden, "team access required")
		return
	}

	var t teamJSON
	var showPub int
	err = h.db.QueryRowContext(r.Context(),
		`SELECT id, name, slug, description, logo_url, location, brand_color, show_members_publicly, created_at FROM teams WHERE id = ?`, id).
		Scan(&t.ID, &t.Name, &t.Slug, &t.Description, &t.LogoURL, &t.Location, &t.BrandColor, &showPub, &t.CreatedAt)
	if err == nil {
		t.ShowMembersPublicly = showPub != 0
	}
	if errors.Is(err, sql.ErrNoRows) {
		h.writeError(w, http.StatusNotFound, "team not found")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "get team", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	t.MyRole = myRole

	rows, err := h.db.QueryContext(r.Context(), `
		SELECT u.id, u.name, u.email, COALESCE(u.avatar_url,''), tm.role, tm.routing_priority, u.archived_at
		FROM team_members tm JOIN users u ON u.id = tm.user_id
		WHERE tm.team_id = ?
		ORDER BY tm.routing_priority ASC, u.name ASC`, id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "get team members", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	t.Members = []teamMemberJSON{}
	for rows.Next() {
		var m teamMemberJSON
		var archivedAt sql.NullString
		if err := rows.Scan(&m.ID, &m.Name, &m.Email, &m.AvatarURL, &m.Role, &m.RoutingPriority, &archivedAt); err != nil {
			continue
		}
		m.Archived = archivedAt.Valid
		t.Members = append(t.Members, m)
	}
	t.MemberCount = len(t.Members)
	h.writeJSON(w, http.StatusOK, t)
}

// PatchTeam handles PATCH /v1/teams/{id} (admin or team head). Body: {name?, slug?}.
func (h *Handler) PatchTeam(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id := r.PathValue("id")
	allowed, err := h.canManageTeam(r.Context(), user, id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "patch team: auth lookup", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !allowed {
		h.writeError(w, http.StatusForbidden, "admin or team head access required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var req struct {
		Name                *string `json:"name"`
		Slug                *string `json:"slug"`
		Description         *string `json:"description"`
		LogoURL             *string `json:"logo_url"`
		Location            *string `json:"location"`
		BrandColor          *string `json:"brand_color"`
		ShowMembersPublicly *bool   `json:"show_members_publicly"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	var sets []string
	var args []any
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			h.writeError(w, http.StatusBadRequest, "name cannot be empty")
			return
		}
		sets = append(sets, "name = ?")
		args = append(args, name)
	}
	if req.Slug != nil {
		slug := slugify(*req.Slug)
		if slug == "" {
			h.writeError(w, http.StatusBadRequest, "slug cannot be empty")
			return
		}
		sets = append(sets, "slug = ?")
		args = append(args, slug)
	}
	if req.Description != nil {
		sets = append(sets, "description = ?")
		args = append(args, strings.TrimSpace(*req.Description))
	}
	if req.LogoURL != nil {
		sets = append(sets, "logo_url = ?")
		args = append(args, strings.TrimSpace(*req.LogoURL))
	}
	if req.Location != nil {
		sets = append(sets, "location = ?")
		args = append(args, strings.TrimSpace(*req.Location))
	}
	if req.BrandColor != nil {
		sets = append(sets, "brand_color = ?")
		args = append(args, strings.TrimSpace(*req.BrandColor))
	}
	if req.ShowMembersPublicly != nil {
		sets = append(sets, "show_members_publicly = ?")
		if *req.ShowMembersPublicly {
			args = append(args, 1)
		} else {
			args = append(args, 0)
		}
	}
	if len(sets) == 0 {
		h.writeError(w, http.StatusBadRequest, "nothing to update")
		return
	}
	args = append(args, id)
	res, err := h.db.ExecContext(r.Context(),
		"UPDATE teams SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...) // #nosec G202 -- sets is built above from hardcoded "col = ?" literals only; every value is bound via args...
	if err != nil {
		if db.IsUniqueViolation(err) {
			h.writeError(w, http.StatusConflict, "a team with that slug already exists")
			return
		}
		h.logger.ErrorContext(r.Context(), "patch team", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		h.writeError(w, http.StatusNotFound, "team not found")
		return
	}
	h.GetTeam(w, r)
}

// DeleteTeam handles DELETE /v1/teams/{id} (admin). team_members cascade and
// event_types.team_id is set NULL via the schema's foreign keys.
func (h *Handler) DeleteTeam(w http.ResponseWriter, r *http.Request) {
	admin, ok := userFromContext(r.Context())
	if !ok || !admin.IsAdmin {
		h.writeError(w, http.StatusForbidden, "admin access required")
		return
	}
	id := r.PathValue("id")
	res, err := h.db.ExecContext(r.Context(), `DELETE FROM teams WHERE id = ?`, id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "delete team", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		h.writeError(w, http.StatusNotFound, "team not found")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// AddTeamMember handles POST /v1/teams/{id}/members (admin or team head).
// Body: {user_id, routing_priority?, role?}. The user must be an existing, active member.
func (h *Handler) AddTeamMember(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	teamID := r.PathValue("id")
	allowed, err := h.canManageTeam(r.Context(), user, teamID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "add team member: auth lookup", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !allowed {
		h.writeError(w, http.StatusForbidden, "admin or team head access required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var req struct {
		UserID          string `json:"user_id"`
		RoutingPriority int    `json:"routing_priority"`
		Role            string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.UserID == "" {
		h.writeError(w, http.StatusBadRequest, "user_id is required")
		return
	}
	role := "member"
	if req.Role != "" {
		if req.Role != "owner" && req.Role != "member" {
			h.writeError(w, http.StatusBadRequest, "role must be 'owner' or 'member'")
			return
		}
		role = req.Role
	}

	var teamExists int
	if err := h.db.QueryRowContext(r.Context(), `SELECT 1 FROM teams WHERE id = ?`, teamID).Scan(&teamExists); errors.Is(err, sql.ErrNoRows) {
		h.writeError(w, http.StatusNotFound, "team not found")
		return
	} else if err != nil {
		h.logger.ErrorContext(r.Context(), "add team member: team lookup", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	var archivedAt sql.NullString
	if err := h.db.QueryRowContext(r.Context(), `SELECT archived_at FROM users WHERE id = ?`, req.UserID).Scan(&archivedAt); errors.Is(err, sql.ErrNoRows) {
		h.writeError(w, http.StatusBadRequest, "user not found")
		return
	} else if err != nil {
		h.logger.ErrorContext(r.Context(), "add team member: user lookup", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if archivedAt.Valid {
		h.writeError(w, http.StatusBadRequest, "cannot add an archived member to a team")
		return
	}

	if _, err := h.db.ExecContext(r.Context(), `
		INSERT INTO team_members (id, team_id, user_id, role, routing_priority)
		VALUES (?, ?, ?, ?, ?)`,
		uid.New(), teamID, req.UserID, role, req.RoutingPriority); err != nil {
		if db.IsUniqueViolation(err) {
			h.writeError(w, http.StatusConflict, "that user is already in this team")
			return
		}
		h.logger.ErrorContext(r.Context(), "add team member", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.GetTeam(w, r)
}

// UpdateTeamMember handles PATCH /v1/teams/{id}/members/{userId} (admin or team head).
// Body: {routing_priority?, role?}.
func (h *Handler) UpdateTeamMember(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	teamID := r.PathValue("id")
	allowed, err := h.canManageTeam(r.Context(), user, teamID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "update team member: auth lookup", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !allowed {
		h.writeError(w, http.StatusForbidden, "admin or team head access required")
		return
	}
	userID := r.PathValue("userId")
	r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
	var req struct {
		RoutingPriority *int    `json:"routing_priority"`
		Role            *string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	var sets []string
	var args []any
	if req.RoutingPriority != nil {
		sets = append(sets, "routing_priority = ?")
		args = append(args, *req.RoutingPriority)
	}
	if req.Role != nil {
		if *req.Role != "owner" && *req.Role != "member" {
			h.writeError(w, http.StatusBadRequest, "role must be 'owner' or 'member'")
			return
		}
		sets = append(sets, "role = ?")
		args = append(args, *req.Role)
	}
	if len(sets) == 0 {
		h.writeError(w, http.StatusBadRequest, "routing_priority or role is required")
		return
	}
	args = append(args, teamID, userID)
	res, err := h.db.ExecContext(r.Context(),
		"UPDATE team_members SET "+strings.Join(sets, ", ")+" WHERE team_id = ? AND user_id = ?", args...) // #nosec G202 -- sets is built from hardcoded literals only
	if err != nil {
		h.logger.ErrorContext(r.Context(), "update team member", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		h.writeError(w, http.StatusNotFound, "team member not found")
		return
	}
	h.GetTeam(w, r)
}

// RemoveTeamMember handles DELETE /v1/teams/{id}/members/{userId} (admin or team head).
func (h *Handler) RemoveTeamMember(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	teamID := r.PathValue("id")
	allowed, err := h.canManageTeam(r.Context(), user, teamID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "remove team member: auth lookup", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !allowed {
		h.writeError(w, http.StatusForbidden, "admin or team head access required")
		return
	}
	userID := r.PathValue("userId")
	res, err := h.db.ExecContext(r.Context(),
		`DELETE FROM team_members WHERE team_id = ? AND user_id = ?`, teamID, userID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "remove team member", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		h.writeError(w, http.StatusNotFound, "team member not found")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
