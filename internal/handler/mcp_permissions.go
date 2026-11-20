package handler

import (
	"context"
	"database/sql"
	"errors"
	"github.com/calnode/calnode/internal/access"
	"net/http"
	"strings"
	"time"
)

var mcpActions = []string{"event_types.read", "event_types.create", "event_types.update", "event_types.delete", "event_types.links", "event_types.custom_times", "team_event_types.read", "team_event_types.create", "team_event_types.update", "team_event_types.delete", "team_event_types.links", "team_event_types.custom_times", "dynamic_links.create", "availability.read", "availability.update", "bookings.read", "bookings.create", "bookings.reschedule", "bookings.cancel", "notes.read", "transcripts.read", "profile.read", "profile.update", "teams.read", "teams.create", "teams.update", "teams.delete", "settings.read", "settings.update", "branding.read", "branding.update", "integrations.read", "integrations.update", "logs.read"}

func (h *Handler) mcpPrincipal(ctx context.Context, token string) (AuthUser, time.Time, error) {
	var u AuthUser
	var exp time.Time
	var id, expires, resource string
	var grants sql.NullString
	var legacy bool
	err := h.db.QueryRowContext(ctx, `SELECT t.id,t.user_id,t.expires_at,t.resource,t.permissions_json,t.is_legacy FROM oauth_access_tokens t JOIN users u ON u.id=t.user_id WHERE t.token_hash=? AND u.archived_at IS NULL`, hashAPIKey(token)).Scan(&id, &u.ID, &expires, &resource, &grants, &legacy)
	if err == nil {
		var e error
		exp, e = time.Parse(time.RFC3339, expires)
		if e != nil || !time.Now().Before(exp) || (resource != "" && resource != h.baseURL+"/mcp") {
			return u, exp, errors.New("invalid MCP token")
		}
		if resource == "" && grants.Valid && !legacy {
			return u, exp, errors.New("token audience missing")
		}
		current, e := access.Load(ctx, h.db, u.ID)
		if e != nil {
			return u, exp, e
		}
		if !grants.Valid {
			grants.String = current.JSON()
			_, e = h.db.ExecContext(ctx, `UPDATE oauth_access_tokens SET permissions_json=?,is_legacy=1,resource=? WHERE id=? AND permissions_json IS NULL`, grants.String, h.baseURL+"/mcp", id)
			if e != nil {
				return u, exp, e
			}
		}
		if resource == "" && legacy {
			if _, e = h.db.ExecContext(ctx, `UPDATE oauth_access_tokens SET resource=? WHERE id=? AND resource=''`, h.baseURL+"/mcp", id); e != nil {
				return u, exp, e
			}
		}
		u.Permissions = access.Intersect(current, access.Parse(grants.String))
		u.CredentialID = id
		_, _ = h.db.ExecContext(ctx, `UPDATE oauth_access_tokens SET last_used_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339), id)
	} else {
		var keyID string
		if h.db.QueryRowContext(ctx, `SELECT ak.id,u.id,u.is_admin,u.is_owner FROM api_keys ak JOIN users u ON u.id=ak.user_id WHERE ak.key_hash=? AND u.archived_at IS NULL`, hashAPIKey(token)).Scan(&keyID, &u.ID, &u.IsAdmin, &u.IsOwner) != nil {
			return u, exp, errors.New("invalid credential")
		}
		if e := h.loadAuthorization(ctx, &u, keyID); e != nil {
			return u, exp, e
		}
		_, _ = h.db.ExecContext(ctx, `UPDATE api_keys SET last_used_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339), keyID)
		exp = time.Now().Add(mcpAccessTokenTTL)
	}
	if !u.Permissions.Has("mcp.connect") {
		return u, exp, errors.New("MCP connections are not allowed by this role or credential")
	}
	return u, exp, nil
}

func (h *Handler) authorizeMCPTool(ctx context.Context, action, kind, id string) (context.Context, error) {
	c, ok := mcpCallerFromContext(ctx)
	if !ok {
		return ctx, nil
	} // local stdio operator
	if c.Permissions == nil {
		return ctx, nil
	} // legacy internal callers in tests
	if action == "event_types.read" && id != "" {
		action = access.EventPermission(ctx, h.db, action, id)
	}
	if action == "event_types.read" && kind == "" && !c.Permissions.Has(action) && c.Permissions.Has("team_event_types.read") {
		action = "team_event_types.read"
	}
	if !c.Permissions.Has(action) {
		return ctx, errors.New("permission required: " + action)
	}
	scope := c.Permissions[action]
	if kind == "booking" && id != "" && !access.BookingAllowed(ctx, h.db, c.UserID, scope, id) {
		return ctx, errors.New("booking not found")
	}
	if kind == "event" && id != "" {
		allowed := access.EventActionAllowed(ctx, h.db, c.UserID, c.Permissions, "read", id)
		if !allowed {
			return ctx, errors.New("event type not found")
		}
	}
	c.Operation = action
	c.IsAdmin = scope == "all" || (kind != "" && id != "") // a concrete resource was already authorized above
	return withMCPCaller(ctx, c), nil
}

func selectedMCPGrants(current access.Grants, ar authRequest, r *http.Request) (access.Grants, error) {
	out := access.Grants{"mcp.connect": "own"}
	selected := r.PostForm["permissions"]
	if r.PostForm.Get("permissions_present") == "" {
		for _, id := range []string{"event_types.read", "availability.read", "bookings.read"} {
			if current.Has(id) {
				selected = append(selected, id)
			}
		}
	}
	for _, id := range selected {
		allowed := false
		for _, p := range mcpActions {
			if p == id {
				allowed = true
			}
		}
		if !allowed || !current.Has(id) {
			return nil, errors.New("requested permission is unavailable")
		}
		if ar.Scope != "mcp" && !strings.Contains(" "+ar.Scope+" ", " "+id+" ") {
			return nil, errors.New("permission was not requested by the client")
		}
		out[id] = current[id]
	}
	return out, nil
}

// Profiles are one-time checkbox selections, never stored roles or authority.
func mcpProfileActions(profile string) map[string]bool {
	out := map[string]bool{}
	read := []string{"event_types.read", "team_event_types.read", "availability.read", "bookings.read"}
	scheduling := []string{"bookings.create", "bookings.reschedule", "bookings.cancel", "event_types.links", "event_types.custom_times", "team_event_types.links", "team_event_types.custom_times", "dynamic_links.create"}
	for _, p := range read {
		out[p] = true
	}
	if profile == "scheduling" || profile == "event_manager" || profile == "workspace_operator" {
		for _, p := range scheduling {
			out[p] = true
		}
	}
	if profile == "event_manager" || profile == "workspace_operator" {
		for _, p := range []string{"profile.read", "profile.update", "teams.read", "availability.update", "integrations.read", "integrations.update", "event_types.create", "event_types.update", "event_types.delete", "team_event_types.create", "team_event_types.update", "team_event_types.delete"} {
			out[p] = true
		}
	}
	if profile == "workspace_operator" {
		for _, p := range mcpActions {
			if p != "notes.read" && p != "transcripts.read" {
				out[p] = true
			}
		}
	}
	return out
}
