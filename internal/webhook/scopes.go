package webhook

import (
	"context"
	"database/sql"
	"encoding/json"
)

// Scopes are ORed, then intersected with live resource authorization on every send.
type Scope struct {
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`
}

func (s *Service) matchesScopes(ctx context.Context, raw sql.NullString, legacy sql.NullString, bookingID, eventID, linkID string) bool {
	if !raw.Valid {
		return !legacy.Valid || legacy.String == "" || legacy.String == eventID
	}
	var scopes []Scope
	if json.Unmarshal([]byte(raw.String), &scopes) != nil || len(scopes) == 0 {
		return false
	}
	var owner, team, host, creator string
	if linkID != "" {
		_ = s.db.QueryRowContext(ctx, `SELECT created_by_user_id FROM single_use_tokens WHERE id=?`, linkID).Scan(&creator)
	}
	if bookingID != "" {
		if s.db.QueryRowContext(ctx, `SELECT e.id,e.user_id,COALESCE(e.team_id,''),b.host_id FROM bookings b JOIN event_types e ON e.id=b.event_type_id WHERE b.id=?`, bookingID).Scan(&eventID, &owner, &team, &host) != nil {
			return false
		}
	} else if eventID != "" {
		if s.db.QueryRowContext(ctx, `SELECT id,user_id,COALESCE(team_id,'') FROM event_types WHERE id=? OR slug=?`, eventID, eventID).Scan(&eventID, &owner, &team) != nil {
			return false
		}
	}
	for _, scope := range scopes {
		switch scope.Type {
		case "instance":
			return eventID != ""
		case "event":
			if eventID != "" && eventID == scope.ID {
				return true
			}
		case "team":
			if team != "" && team == scope.ID {
				return true
			}
		case "user":
			if scope.ID != "" && (scope.ID == owner || scope.ID == host || scope.ID == creator) {
				return true
			}
			if bookingID != "" {
				var n int
				if s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM booking_hosts WHERE booking_id=? AND user_id=?`, bookingID, scope.ID).Scan(&n) == nil && n > 0 {
					return true
				}
			}
		}
	}
	return false
}
