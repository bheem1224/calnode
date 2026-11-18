package webhook

import (
	"context"
	"database/sql"
	"github.com/calnode/calnode/internal/access"
	"strings"
)

// DeliveryAuthorized is evaluated both before enqueue and immediately before an
// HTTP delivery/retry. A queued payload is not a permanent authorization grant.
func (s *Service) DeliveryAuthorized(ctx context.Context, id, bookingID, eventID, event string) bool {
	var owner string
	var raw sql.NullString
	var active bool
	if s.db.QueryRowContext(ctx, `SELECT user_id,permissions_json,is_active FROM webhooks WHERE id=?`, id).Scan(&owner, &raw, &active) != nil || !active {
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
		permission = "event_types.links"
	}
	scope := grants[permission]
	if access.Rank(grants["webhooks.create"]) < access.Rank(scope) {
		scope = grants["webhooks.create"]
	}
	if scope == "" {
		return false
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
