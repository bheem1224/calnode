package calendar

import (
	"context"
	"database/sql"
	"github.com/calnode/calnode/internal/connstore"
)

// Pin the destination before the first external write. Changing preferences or
// restarting between an accepted write and its response cannot create a second
// event in another account/calendar on the next reconciliation.
func (s *Service) createIntent(ctx context.Context, user string, p CreateEventParams) (provider, reference string, err error) {
	read := func() error {
		return s.db.QueryRowContext(ctx, `SELECT provider,calendar_ref FROM calendar_event_intents WHERE user_id=? AND operation_key=?`, user, p.IdempotencyKey).Scan(&provider, &reference)
	}
	if err = read(); err == nil {
		return
	}
	if err != sql.ErrNoRows {
		return
	}
	err = nil
	var calendarID, account string
	resolved := false
	if p.DestinationCalendarID != "" {
		provider, calendarID, account, err = connstore.CalendarTarget(ctx, s.db, user, p.DestinationCalendarID)
		if err == nil {
			resolved = true
		} else if connstore.IsCalendarReference(p.DestinationCalendarID) || err != sql.ErrNoRows {
			return
		}
	}
	if !resolved {
		err = s.db.QueryRowContext(ctx, `SELECT provider,calendar_id,account_email FROM calendar_connections WHERE user_id=? AND is_destination=1 LIMIT 1`, user).Scan(&provider, &calendarID, &account)
		if err == sql.ErrNoRows {
			return "", "", nil
		}
		if err != nil {
			return
		}
		if p.DestinationCalendarID != "" {
			calendarID = p.DestinationCalendarID
		} else {
			var picked string
			var ok bool
			picked, ok, err = connstore.DestinationCalendarID(ctx, s.db, user, provider, account)
			if err != nil {
				return
			}
			if ok {
				calendarID = picked
			}
		}
	}
	if provider != "microsoft" && provider != "google" {
		return "", "", nil
	}
	if calendarID == "" {
		calendarID = "primary"
	}
	reference = connstore.CalendarReference(provider, calendarID, account)
	_, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO calendar_event_intents(user_id,operation_key,provider,calendar_ref)VALUES(?,?,?,?)`, user, p.IdempotencyKey, provider, reference)
	if err != nil {
		return
	}
	err = read()
	return
}
