package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/calnode/calnode/internal/access"
	"github.com/calnode/calnode/internal/webhook"
)

func webhookReadPermission(event string) string {
	switch {
	case event == "booking.answers":
		return "bookings.answers"
	case strings.HasPrefix(event, "recording."):
		return "recordings.read"
	case strings.HasPrefix(event, "transcript."):
		return "transcripts.read"
	case strings.HasPrefix(event, "notes."):
		return "notes.read"
	case strings.HasPrefix(event, "single_use_link."):
		return "event_types.links"
	default:
		return "bookings.read"
	}
}

func (h *Handler) validateWebhookScopes(ctx context.Context, u AuthUser, scopes *[]webhook.Scope, events []string) error {
	if scopes == nil {
		return nil
	}
	if len(*scopes) == 0 || len(*scopes) > 100 {
		return errors.New("choose 1 to 100 webhook scopes")
	}
	g := u.Permissions
	if g == nil {
		var err error
		g, err = access.Load(ctx, h.db, u.ID)
		if err != nil {
			return err
		}
	}
	seen := map[string]bool{}
	for _, scope := range *scopes {
		key := scope.Type + ":" + scope.ID
		if seen[key] {
			return errors.New("duplicate webhook scope")
		}
		seen[key] = true
		if scope.Type != "user" && scope.Type != "team" && scope.Type != "event" && scope.Type != "instance" {
			return errors.New("unknown webhook scope")
		}
		if (scope.Type == "instance") != (scope.ID == "") {
			return errors.New("scope identifier is required except for instance scope")
		}
		var team string
		if scope.Type == "event" {
			if h.db.QueryRowContext(ctx, `SELECT COALESCE(team_id,'') FROM event_types WHERE id=?`, scope.ID).Scan(&team) != nil {
				return errors.New("event type not found")
			}
		}
		if scope.Type == "user" {
			var n int
			if h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id=? AND archived_at IS NULL`, scope.ID).Scan(&n) != nil || n != 1 {
				return errors.New("user not found")
			}
		}
		if scope.Type == "team" {
			var n int
			if h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM teams WHERE id=?`, scope.ID).Scan(&n) != nil || n != 1 {
				return errors.New("team not found")
			}
		}
		for _, event := range events {
			permission := webhookReadPermission(event)
			if permission == "event_types.links" && (scope.Type == "team" || team != "") {
				permission = "team_event_types.links"
			}
			grant := g[permission]
			if access.Rank(g["webhooks.create"]) < access.Rank(grant) {
				grant = g["webhooks.create"]
			}
			if access.Rank(grant) == 0 {
				return errors.New("missing permission for webhook data: " + permission)
			}
			allowed := false
			switch scope.Type {
			case "instance":
				allowed = grant == "all"
				if permission == "event_types.links" {
					allowed = allowed && g["team_event_types.links"] == "all"
				}
			case "user":
				allowed = scope.ID == u.ID || grant == "all" || (grant == "team" && access.Related(ctx, h.db, u.ID, scope.ID))
			case "team":
				if grant == "all" {
					allowed = true
				} else if grant == "team" {
					var n int
					_ = h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM team_members WHERE user_id=? AND team_id=?`, u.ID, scope.ID).Scan(&n)
					allowed = n > 0
				}
			case "event":
				allowed = access.EventAllowed(ctx, h.db, u.ID, grant, scope.ID)
			}
			if !allowed {
				return errors.New("selected webhook scope exceeds the user's permissions")
			}
		}
	}
	return nil
}

func (h *Handler) WebhookScopeOptions(w http.ResponseWriter, r *http.Request) {
	u, _ := userFromContext(r.Context())
	type option struct {
		webhook.Scope
		Name string `json:"name"`
	}
	options := []option{}
	add := func(scope webhook.Scope, name string) {
		if h.validateWebhookScopes(r.Context(), u, &[]webhook.Scope{scope}, []string{"booking.created"}) == nil {
			options = append(options, option{scope, name})
		}
	}
	add(webhook.Scope{Type: "user", ID: u.ID}, u.Name)
	add(webhook.Scope{Type: "instance"}, "")
	// Materialize before permission lookups: SQLite uses a single connection.
	type row struct{ id, name string }
	read := func(query string) []row {
		rows, err := h.db.QueryContext(r.Context(), query)
		if err != nil {
			return nil
		}
		out := []row{}
		for rows.Next() {
			var v row
			if rows.Scan(&v.id, &v.name) == nil {
				out = append(out, v)
			}
		}
		rows.Close()
		return out
	}
	for _, v := range read(`SELECT id,name FROM teams ORDER BY name`) {
		add(webhook.Scope{Type: "team", ID: v.id}, v.name)
	}
	for _, v := range read(`SELECT id,name FROM event_types WHERE archived_at IS NULL ORDER BY name`) {
		add(webhook.Scope{Type: "event", ID: v.id}, v.name)
	}
	if u.Permissions["webhooks.create"] != "own" {
		for _, v := range read(`SELECT id,name FROM users WHERE archived_at IS NULL ORDER BY name`) {
			if v.id != u.ID {
				add(webhook.Scope{Type: "user", ID: v.id}, v.name)
			}
		}
	}
	h.writeJSON(w, 200, map[string]any{"items": options})
}

func webhookScopeEvents(events, fields []string) []string {
	out := append([]string{}, events...)
	for _, field := range fields {
		if field == "answers" {
			out = append(out, "booking.answers")
			break
		}
	}
	return out
}
