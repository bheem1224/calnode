package calendar

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/calnode/calnode/internal/providerhttp"
)

var ErrObsoleteAssignment = errors.New("calendar operation belongs to a cancelled or replaced booking assignment")

// EventRecoverer reads by the original operation identity. It must never create
// an event, and must fail rather than report absence when credentials are missing.
type EventRecoverer interface {
	RecoverEvent(context.Context, string, string, string) (string, error)
}

func (s *Service) operationLock(user, key string) string {
	return providerhttp.Key("calendar-lifecycle", fmt.Sprintf("%p", s.db), user, key)
}
func (s *Service) liveAssignment(ctx context.Context, user string, p CreateEventParams) error {
	if p.IdempotencyKey != "booking-host:"+p.AssignmentID {
		return ErrObsoleteAssignment
	}
	var live int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookings b WHERE b.id=? AND b.status='confirmed' AND EXISTS(SELECT 1 FROM booking_hosts bh WHERE bh.booking_id=b.id AND bh.user_id=? AND bh.id=?)`, p.BookingID, user, p.AssignmentID).Scan(&live)
	if err != nil {
		return err
	}
	if live != 1 {
		return ErrObsoleteAssignment
	}
	return nil
}
func (s *Service) liveIntent(ctx context.Context, user string, p CreateEventParams) error {
	var retired int
	if err := s.db.QueryRowContext(ctx, `SELECT retired FROM calendar_event_intents WHERE user_id=? AND operation_key=?`, user, p.IdempotencyKey).Scan(&retired); err != nil {
		return err
	}
	if retired != 0 {
		return ErrObsoleteAssignment
	}
	if p.BookingID != "" {
		return s.liveAssignment(ctx, user, p)
	}
	return nil
}

type cleanupTask struct {
	id, booking, user, provider, calendar, event, operation, created string
	attempts                                                         int
}

// ReconcileCleanup drains durable retirement work independently of whether the
// original booking/seat still exists. Errors remain pending, including revoked
// authorization; reconnecting allows the same target to be retried later.
func (s *Service) ReconcileCleanup(ctx context.Context) error {
	return s.cleanupPending(ctx, "")
}

func (s *Service) CleanupBooking(ctx context.Context, booking string) error {
	return s.cleanupPending(ctx, booking)
}

func (s *Service) cleanupPending(ctx context.Context, booking string) error {
	// Also discover legacy/imported rows inserted directly in cancelled state,
	// without a status transition. INSERT OR IGNORE preserves completed tasks.
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO calendar_event_cleanup(id,booking_id,user_id,provider,calendar_ref,event_id,operation_key) SELECT id,booking_id,user_id,provider,calendar_ref,event_id,operation_key FROM calendar_cleanup_targets WHERE booking_id IN(SELECT id FROM bookings WHERE status='cancelled') AND (?='' OR booking_id=?) AND (event_id<>'' OR operation_key<>'')`, booking, booking); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE calendar_event_intents SET retired=1 WHERE retired=0 AND booking_id IN(SELECT id FROM bookings WHERE status='cancelled') AND (?='' OR booking_id=?)`, booking, booking); err != nil {
		return err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,booking_id,user_id,provider,calendar_ref,event_id,operation_key,created_at,attempts FROM calendar_event_cleanup WHERE done=0 AND next_attempt_at<=? AND (?='' OR booking_id=?) ORDER BY created_at,id LIMIT 100`, time.Now().UTC().Format(time.RFC3339Nano), booking, booking)
	if err != nil {
		return err
	}
	var tasks []cleanupTask
	for rows.Next() {
		var t cleanupTask
		if err = rows.Scan(&t.id, &t.booking, &t.user, &t.provider, &t.calendar, &t.event, &t.operation, &t.created, &t.attempts); err != nil {
			rows.Close()
			return err
		}
		tasks = append(tasks, t)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var failures []error
	for _, task := range tasks {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		key := task.operation
		if key == "" {
			key = "cleanup:" + task.id
		}
		err = providerhttp.Serialize(ctx, s.operationLock(task.user, key), func() error { return s.cleanupOne(ctx, task) })
		if err != nil {
			delay := time.Minute * time.Duration(1<<min(task.attempts, 8))
			if delay > 6*time.Hour {
				delay = 6 * time.Hour
			}
			next := time.Now().Add(delay)
			var deferred interface{ RetryAt() time.Time }
			if errors.As(err, &deferred) && deferred.RetryAt().After(next) {
				next = deferred.RetryAt()
			}
			// Deliberately store a safe summary, never provider bodies or credentials.
			_, saveErr := s.db.ExecContext(ctx, `UPDATE calendar_event_cleanup SET attempts=attempts+1,next_attempt_at=?,last_error='Calendar cleanup pending; check provider access' WHERE id=? AND done=0`, next.UTC().Format(time.RFC3339Nano), task.id)
			failures = append(failures, fmt.Errorf("calendar cleanup %s: %w", task.id, err))
			if saveErr != nil {
				failures = append(failures, saveErr)
			}
		}
	}
	return errors.Join(failures...)
}

func (s *Service) cleanupOne(ctx context.Context, t cleanupTask) error {
	var done int
	if err := s.db.QueryRowContext(ctx, `SELECT event_id,done FROM calendar_event_cleanup WHERE id=?`, t.id).Scan(&t.event, &done); err != nil {
		return err
	}
	if done != 0 {
		return nil
	}
	if t.operation != "" {
		var saved string
		var started int
		if err := s.db.QueryRowContext(ctx, `SELECT event_id,write_started FROM calendar_event_intents WHERE user_id=? AND operation_key=?`, t.user, t.operation).Scan(&saved, &started); err != nil && err != sql.ErrNoRows {
			return err
		} else if err == nil {
			if saved != "" {
				t.event = saved
			}
			if started == 0 && t.event == "" {
				_, err = s.db.ExecContext(ctx, `UPDATE calendar_event_cleanup SET done=1,last_error='' WHERE id=?`, t.id)
				return err
			}
		}
	}
	pr := s.providers[t.provider]
	if pr == nil && t.provider == "" && t.event != "" {
		pr = s.providerForEvent(ctx, t.user, t.event, "")
	}
	if pr == nil {
		return errors.New("original calendar provider unavailable")
	}
	connected, err := pr.Connected(ctx, t.user)
	if err != nil {
		return err
	}
	if !connected {
		return errors.New("original calendar account needs reconnection")
	}
	if t.event == "" {
		finder, ok := pr.(EventRecoverer)
		if !ok {
			return errors.New("calendar provider cannot recover an unknown event")
		}
		t.event, err = finder.RecoverEvent(ctx, t.user, t.calendar, t.operation)
		if err != nil {
			return err
		}
		if t.event == "" {
			// Keep checking uncertain outcomes for 72 hours. A single absent read
			// immediately after a lost acknowledgement is not proof of no write.
			created, _ := time.Parse(time.RFC3339Nano, t.created)
			if created.IsZero() || time.Since(created) < 72*time.Hour {
				return errors.New("unknown create outcome awaiting reconciliation")
			}
		} else if _, err = s.db.ExecContext(ctx, `UPDATE calendar_event_cleanup SET event_id=? WHERE id=?`, t.event, t.id); err != nil {
			return err
		}
	}
	if t.event != "" {
		if err = pr.CancelEvent(ctx, t.user, t.calendar, t.event); err != nil {
			return err
		}
		connected, err = pr.Connected(ctx, t.user)
		if err != nil {
			return err
		}
		if !connected {
			return errors.New("calendar disconnected during cleanup")
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE calendar_event_cleanup SET done=1,last_error='' WHERE id=?`, t.id); err != nil {
		return err
	}
	if t.event != "" {
		if _, err = tx.ExecContext(ctx, `UPDATE booking_hosts SET external_event_id=NULL WHERE booking_id=? AND user_id=? AND external_event_id=? AND booking_id IN(SELECT id FROM bookings WHERE status='cancelled')`, t.booking, t.user, t.event); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE bookings SET external_event_id=NULL WHERE id=? AND status='cancelled' AND external_event_id=?`, t.booking, t.event); err != nil {
			return err
		}
	}
	return tx.Commit()
}
