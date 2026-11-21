package webhook

import (
	"context"
	"database/sql"
	"github.com/calnode/calnode/internal/access"
	"strings"
)

// DeliveryAuthorized is evaluated both before enqueue and immediately before an
// HTTP delivery/retry. A queued payload is not a permanent authorization grant.
func (s *Service) DeliveryAuthorized(ctx context.Context, id, bookingID, eventID, event string, linkIDs ...string) bool {
	var owner string
	var raw, scopes, legacy sql.NullString
	var active bool
	if s.db.QueryRowContext(ctx, `SELECT user_id,permissions_json,is_active,scopes_json,event_type_id FROM webhooks WHERE id=?`, id).Scan(&owner, &raw, &active, &scopes, &legacy) != nil || !active {
		return false
	}
	if bookingID != "" {
		_ = s.db.QueryRowContext(ctx, `SELECT event_type_id FROM bookings WHERE id=?`, bookingID).Scan(&eventID)
	} else if eventID != "" {
		var resolved string
		if s.db.QueryRowContext(ctx, `SELECT id FROM event_types WHERE id=? OR slug=?`, eventID, eventID).Scan(&resolved) == nil {
			eventID = resolved
		}
	}
	linkID := ""
	if len(linkIDs) > 0 && strings.HasPrefix(event, "single_use_link.") {
		linkID = linkIDs[0]
	}
	if !s.matchesScopes(ctx, scopes, legacy, bookingID, eventID, linkID) {
		return false
	}
	current, err := access.Load(ctx, s.db, owner)
	if err != nil {
		return false
	}
	if !raw.Valid {
		return false
	}
	grants := access.Intersect(current, access.Parse(raw.String))
	if !grants.Has("webhooks.create") {
		return false
	}
	permission := "bookings.read"
	if event == "booking.answers" {
		permission = "bookings.answers"
	}
	if strings.HasPrefix(event, "recording.") {
		permission = "recordings.read"
	}
	if strings.HasPrefix(event, "transcript.") {
		permission = "transcripts.read"
	}
	if strings.HasPrefix(event, "notes.") {
		permission = "notes.read"
	}
	if strings.HasPrefix(event, "single_use_link.") {
		permission = access.EventPermission(ctx, s.db, "event_types.links", eventID)
	}
	scope := grants[permission]
	if access.Rank(grants["webhooks.create"]) < access.Rank(scope) {
		scope = grants["webhooks.create"]
	}
	if scope == "" {
		return false
	}
	if strings.HasPrefix(event, "single_use_link.") && eventID != "" {
		g := access.Grants{}
		for p, v := range grants {
			g[p] = v
		}
		if scope == "own" {
			var eventOwner, creator string
			_ = s.db.QueryRowContext(ctx, `SELECT user_id FROM event_types WHERE id=?`, eventID).Scan(&eventOwner)
			if linkID != "" {
				_ = s.db.QueryRowContext(ctx, `SELECT created_by_user_id FROM single_use_tokens WHERE id=?`, linkID).Scan(&creator)
			}
			if eventOwner != owner && creator != owner {
				return false
			}
		} else {
			g[permission] = scope
		}
		return access.EventActionAllowed(ctx, s.db, owner, g, "links", eventID)
	}

	if bookingID != "" {
		return access.BookingAllowed(ctx, s.db, owner, scope, bookingID)
	}
	if eventID != "" {
		return access.EventAllowed(ctx, s.db, owner, scope, eventID)
	}
	// Synthetic non-booking jobs remain constrained to the hook's own user.
	return grants.Has(permission)
}
