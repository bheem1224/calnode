// Package access defines the permission vocabulary shared by interactive users,
// delegated credentials, MCP tools, and background webhook delivery.
package access

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

type Grants map[string]string
type Permission struct {
	ID        string   `json:"id"`
	Group     string   `json:"group"`
	Scopes    []string `json:"scopes"`
	Delegable bool     `json:"delegable"`
}

var Catalog = func() []Permission {
	groups := []struct {
		name, actions string
		global        bool
	}{
		{"profile", "read update", false},
		{"dynamic_links", "create", false},
		{"event_types", "read create update delete links custom_times", false},
		{"team_event_types", "read create update delete links custom_times", false},
		{"logs", "read", true},
		{"bookings", "read create reschedule cancel approve reassign answers", false},
		{"availability", "read update", false},
		{"members", "read invite_member invite_admin update_role archive password reset_mfa", true},
		{"roles", "read manage", true},
		{"teams", "read create update delete members", false},
		{"api_keys", "read create revoke manage", false},
		{"mcp", "connect manage", false},
		{"webhooks", "read create update delete deliveries rotate", false},
		{"integrations", "read update", false},
		{"settings", "read update security", true},
		{"branding", "read update", true},
		{"recordings", "read delete", false},
		{"transcripts", "read", false},
		{"notes", "read update", false},
		{"impersonation", "use audit", true},
	}
	out := []Permission{}
	for _, g := range groups {
		for _, a := range strings.Fields(g.actions) {
			scopes := []string{"own", "team", "all"}
			if g.global || (g.name == "teams" && (a == "create" || a == "delete")) {
				scopes = []string{"all"}
			}
			if g.name == "team_event_types" {
				scopes = []string{"team", "all"}
			}
			if g.name == "event_types" {
				scopes = []string{"own", "all"}
			}
			if g.name == "profile" || (g.name == "availability" && a == "update") || g.name == "integrations" || (g.name == "api_keys" && a == "create") || (g.name == "mcp" && a == "connect") {
				scopes = []string{"own"}
			}
			id := g.name + "." + a
			delegable := id != "api_keys.create" && id != "api_keys.manage" && id != "roles.manage" && id != "settings.security" && id != "impersonation.use" && id != "members.reset_mfa"
			out = append(out, Permission{id, g.name, scopes, delegable})
		}
	}
	return out
}()

func Rank(s string) int {
	switch s {
	case "own":
		return 1
	case "team":
		return 2
	case "all":
		return 3
	}
	return 0
}
func (g Grants) Has(p string) bool { return Rank(g[p]) > 0 }
func (g Grants) JSON() string      { b, _ := json.Marshal(g); return string(b) }
func Parse(raw string) Grants {
	g := Grants{}
	if json.Unmarshal([]byte(raw), &g) != nil || g == nil {
		return Grants{}
	}
	// Old personal-event team scopes included assigned hosts. Narrow them to
	// own resources; never turn them into a grant over team-owned event types.
	for id, scope := range g {
		if strings.HasPrefix(id, "event_types.") && scope == "team" {
			g[id] = "own"
		}
	}
	return g
}
func Full() Grants {
	g := Grants{}
	for _, p := range Catalog {
		g[p.ID] = p.Scopes[len(p.Scopes)-1]
	}
	return g
}
func Member(legacy bool) Grants {
	g := Grants{}
	for _, p := range Catalog {
		switch p.Group {
		case "profile", "event_types", "bookings", "availability", "teams", "integrations", "recordings", "transcripts", "notes":
			g[p.ID] = "own"
		}
	}
	delete(g, "teams.create")
	delete(g, "teams.delete")
	g["event_types.read"] = "own"
	g["team_event_types.read"] = "team"
	g["team_event_types.links"] = "team"
	g["team_event_types.custom_times"] = "team"
	g["dynamic_links.create"] = "all"
	g["mcp.connect"] = "own"
	g["mcp.manage"] = "own"
	g["api_keys.read"] = "own"
	g["api_keys.revoke"] = "own"
	if legacy {
		for _, p := range Catalog {
			if p.Group == "api_keys" || p.Group == "webhooks" {
				g[p.ID] = "own"
			}
		}
	}
	return g
}
func Intersect(a, b Grants) Grants {
	g := Grants{}
	for p, s := range a {
		if Rank(b[p]) > 0 {
			if Rank(b[p]) < Rank(s) {
				s = b[p]
			}
			g[p] = s
		}
	}
	return g
}
func Delegable(g Grants) Grants {
	out := Grants{}
	for _, p := range Catalog {
		if p.Delegable && g.Has(p.ID) {
			out[p.ID] = g[p.ID]
		}
	}
	return out
}
func Validate(g, ceiling Grants, delegated bool) error {
	for id, s := range g {
		found := false
		for _, p := range Catalog {
			if p.ID != id {
				continue
			}
			found = true
			valid := false
			for _, v := range p.Scopes {
				if v == s {
					valid = true
				}
			}
			if !valid || Rank(s) > Rank(ceiling[id]) || (delegated && !p.Delegable) {
				return errors.New("permission is not grantable: " + id)
			}
		}
		if !found {
			return errors.New("unknown permission: " + id)
		}
	}
	return nil
}

// Load always reads current membership; a credential never caches its owner's role.
type Querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func Load(ctx context.Context, db Querier, userID string) (Grants, error) {
	var admin, owner, legacy bool
	var roleID string
	var raw sql.NullString
	var users, settings, teams, imp sql.NullBool
	err := db.QueryRowContext(ctx, `SELECT u.is_admin,u.is_owner,u.legacy_permissions,COALESCE(u.custom_role_id,''),r.permissions_json,r.can_manage_users,r.can_manage_settings,r.can_manage_teams,r.allow_impersonation FROM users u LEFT JOIN roles r ON r.id=u.custom_role_id WHERE u.id=? AND u.archived_at IS NULL`, userID).Scan(&admin, &owner, &legacy, &roleID, &raw, &users, &settings, &teams, &imp)
	if err != nil {
		return nil, err
	}
	if admin || owner {
		return Full(), nil
	}
	if raw.Valid {
		g := Parse(raw.String)
		g["profile.read"] = "own"
		g["profile.update"] = "own"
		return g, nil
	}
	if roleID != "" {
		return LegacyRole(users.Bool, settings.Bool, teams.Bool, imp.Bool), nil
	}
	return defaultMember(ctx, db, userID, legacy), nil
}

func Related(ctx context.Context, db *sql.DB, userID, otherID string) bool {
	if userID == otherID {
		return true
	}
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM team_members a JOIN team_members b ON a.team_id=b.team_id WHERE a.user_id=? AND b.user_id=?`, userID, otherID).Scan(&n)
	return err == nil && n > 0
}
func EventAllowed(ctx context.Context, db *sql.DB, userID, scope, id string) bool {
	if Rank(scope) == 0 {
		return false
	}
	if scope == "all" {
		return true
	}
	var owner string
	var team sql.NullString
	if db.QueryRowContext(ctx, `SELECT user_id,team_id FROM event_types WHERE id=? OR slug=?`, id, id).Scan(&owner, &team) != nil {
		return false
	}
	if owner == userID {
		return true
	}
	var n int
	if db.QueryRowContext(ctx, `SELECT COUNT(*) FROM event_type_hosts WHERE event_type_id=(SELECT id FROM event_types WHERE id=? OR slug=?) AND user_id=?`, id, id, userID).Scan(&n) == nil && n > 0 {
		return true
	}
	if scope == "team" && team.Valid {
		_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM team_members WHERE team_id=? AND user_id=?`, team.String, userID).Scan(&n)
		return n > 0
	}
	return false
}
func BookingAllowed(ctx context.Context, db *sql.DB, userID, scope, id string) bool {
	if Rank(scope) == 0 {
		return false
	}
	if scope == "all" {
		return true
	}
	var host, eventID string
	if db.QueryRowContext(ctx, `SELECT host_id,event_type_id FROM bookings WHERE id=?`, id).Scan(&host, &eventID) != nil {
		return false
	}
	if host == userID {
		return true
	}
	var n int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM booking_hosts WHERE booking_id=? AND user_id=?`, id, userID).Scan(&n)
	if n > 0 {
		return true
	}
	if scope != "team" {
		return false
	}
	// Match booking.ListFilter: only actual booking participants or current
	// members of the event's team. An event owner/assigned host is not a
	// participant in every booking routed through that reusable event type.
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM event_types e JOIN team_members tm ON tm.team_id=e.team_id WHERE e.id=? AND tm.user_id=?`, eventID, userID).Scan(&n)
	return err == nil && n > 0
}

// Role loads an assignable custom role, including the permissions of pre-migration roles.
func Role(ctx context.Context, db Querier, id string) (Grants, error) {
	var raw sql.NullString
	var users, settings, teams, imp bool
	err := db.QueryRowContext(ctx, `SELECT permissions_json,can_manage_users,can_manage_settings,can_manage_teams,allow_impersonation FROM roles WHERE id=? AND is_system=0`, id).Scan(&raw, &users, &settings, &teams, &imp)
	if err != nil {
		return nil, err
	}
	if raw.Valid {
		return Parse(raw.String), nil
	}
	return LegacyRole(users, settings, teams, imp), nil
}

// EventManageAllowed handles personal event configuration. Being assigned as a
// host never grants editing authority, and team scopes do not apply here.
func EventManageAllowed(ctx context.Context, db *sql.DB, userID, scope, id string) bool {
	if scope == "all" {
		return true
	}
	if scope != "own" {
		return false
	}
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM event_types WHERE (id=? OR slug=?) AND user_id=? AND COALESCE(team_id,'')=''`, id, id, userID).Scan(&n)
	return err == nil && n > 0
}

// LegacyRole preserves old management capabilities without silently granting new delegation powers.
func LegacyRole(users, settings, teams, imp bool) Grants {
	g := Member(true)
	for _, p := range Catalog {
		if p.ID == "members.invite_admin" || p.ID == "members.update_role" {
			continue
		}
		if (users && (p.Group == "members" || p.Group == "roles")) || (settings && (p.Group == "settings" || p.Group == "branding")) || (teams && p.Group == "teams") || (imp && p.ID == "impersonation.use") {
			g[p.ID] = "all"
		}
	}
	return g
}

// DynamicHostsAllowed authorizes one-time delegation independently from publishing
// reusable event types. It works against either a DB or a booking transaction.
func DynamicHostsAllowed(ctx context.Context, q Querier, actor, scope string, hosts []string) bool {
	if Rank(scope) == 0 || len(hosts) < 2 || len(hosts) > 10 {
		return false
	}
	seen := map[string]bool{}
	for _, id := range hosts {
		if seen[id] {
			return false
		}
		seen[id] = true
		var n int
		if q.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id=? AND archived_at IS NULL`, id).Scan(&n) != nil || n != 1 {
			return false
		}
		if scope == "all" || id == actor {
			continue
		}
		if scope != "team" {
			return false
		}
		if q.QueryRowContext(ctx, `SELECT COUNT(*) FROM team_members a JOIN team_members b ON a.team_id=b.team_id WHERE a.user_id=? AND b.user_id=?`, actor, id).Scan(&n) != nil || n == 0 {
			return false
		}
	}
	return true
}

// Built-in Members gain team configuration as current Heads. Custom roles are
// explicit: an omitted team action stays denied, even for a Head.
func defaultMember(ctx context.Context, db Querier, userID string, legacy bool) Grants {
	g := Member(legacy)
	var heads int
	if db.QueryRowContext(ctx, `SELECT COUNT(*) FROM team_members WHERE user_id=? AND role='owner'`, userID).Scan(&heads) == nil && heads > 0 {
		for _, action := range []string{"read", "create", "update", "delete", "links", "custom_times"} {
			g["team_event_types."+action] = "team"
		}
	}
	return g
}

func EventPermission(ctx context.Context, db Querier, permission, id string) string {
	var team string
	if id != "" && db.QueryRowContext(ctx, `SELECT COALESCE(team_id,'') FROM event_types WHERE id=? OR slug=?`, id, id).Scan(&team) == nil && team != "" {
		return "team_event_types." + strings.TrimPrefix(permission, "event_types.")
	}
	return permission
}

func EventListPermission(grants Grants) string {
	if !grants.Has("event_types.read") && grants.Has("team_event_types.read") {
		return "team_event_types.read"
	}
	return "event_types.read"
}

// Operating team links is independent of configuring the reusable event type.
func EventActionAllowed(ctx context.Context, db *sql.DB, userID string, grants Grants, action, id string) bool {
	p := EventPermission(ctx, db, "event_types."+action, id)
	scope := grants[p]
	if !grants.Has(p) {
		return false
	}
	if strings.HasPrefix(p, "team_event_types.") {
		var team string
		if db.QueryRowContext(ctx, `SELECT team_id FROM event_types WHERE id=? OR slug=?`, id, id).Scan(&team) != nil {
			return false
		}
		if action != "read" && action != "links" && action != "custom_times" {
			var can int
			if db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users u WHERE u.id=? AND u.archived_at IS NULL AND (u.is_admin=1 OR u.is_owner=1 OR EXISTS(SELECT 1 FROM team_members tm WHERE tm.user_id=u.id AND tm.team_id=? AND tm.role='owner'))`, userID, team).Scan(&can) != nil || can == 0 {
				return false
			}
		}
		if scope == "all" {
			return true
		}
		if scope != "team" {
			return false
		}
		var n int
		return db.QueryRowContext(ctx, `SELECT COUNT(*) FROM team_members WHERE team_id=? AND user_id=?`, team, userID).Scan(&n) == nil && n > 0
	}
	if scope != "own" && scope != "all" {
		return false
	}
	if action == "read" || action == "links" || action == "custom_times" {
		return EventAllowed(ctx, db, userID, scope, id)
	}
	return EventManageAllowed(ctx, db, userID, scope, id)
}

// EventReadFilter is the shared list boundary for the REST editor and MCP. The
// ordinary admin list stays personal; scope=all widens only grants that allow it.
// It is used with event_types as the outer query and binds every caller value.
func EventReadFilter(userID string, grants Grants, includeAll bool) (string, []any) {
	personal, team := grants["event_types.read"], grants["team_event_types.read"]
	if !includeAll {
		if personal == "all" {
			personal = "own"
		}
		if team == "all" {
			team = "team"
		}
	}
	return `((COALESCE(team_id,'')='' AND (?='all' OR (?='own' AND (user_id=? OR id IN (SELECT event_type_id FROM event_type_hosts WHERE user_id=?))))) OR (COALESCE(team_id,'')<>'' AND (?='all' OR (?='team' AND team_id IN (SELECT team_id FROM team_members WHERE user_id=?)))))`, []any{personal, personal, userID, userID, team, team, userID}
}
