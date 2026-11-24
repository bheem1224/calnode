package handler

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/calnode/calnode/internal/calendar"
)

func TestCalendarTransientOutageHealsWithoutRepeatedSuccessfulSideEffects(t *testing.T) {
	for _, status := range []string{"confirmed", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			var logs bytes.Buffer
			h, database, bookingID := newReconcileFixture(t, status, &logs)
			provider := &stuckProvider{err: errors.New("temporary provider HTTP 503")}
			service := calendar.NewService(database)
			service.Register(provider)
			sweep := func() {
				if status == "confirmed" {
					h.reconcileReschedules(context.Background(), service)
				} else {
					h.reconcileCancellations(context.Background(), service)
				}
			}

			sweep()
			eventID, needsSync := hostRow(t, database)
			if !eventID.Valid || needsSync != 1 {
				t.Fatal("provider outage discarded the retryable event reference")
			}
			provider.mu.Lock()
			provider.err = nil
			provider.mu.Unlock()
			if status == "cancelled" {
				if _, err := database.Exec(`UPDATE calendar_event_cleanup SET next_attempt_at=''`); err != nil {
					t.Fatal(err)
				}
			}
			sweep()
			eventID, needsSync = hostRow(t, database)
			if status == "confirmed" && (!eventID.Valid || needsSync != 0) {
				t.Fatal("reschedule did not heal after the provider recovered")
			}
			if status == "cancelled" && eventID.Valid {
				t.Fatal("successful provider cancellation retained its orphan reference")
			}
			updates, cancels := provider.calls()
			sweep()
			afterUpdates, afterCancels := provider.calls()
			if updates != afterUpdates || cancels != afterCancels {
				t.Fatal("a healed operation was sent to the provider again")
			}
			if status == "confirmed" && updates != 2 || status == "cancelled" && cancels != 2 {
				t.Fatalf("expected one failed attempt and one successful recovery, got %d updates / %d cancels", updates, cancels)
			}
			var currentStatus string
			if err := database.QueryRow(`SELECT status FROM bookings WHERE id=?`, bookingID).Scan(&currentStatus); err != nil || currentStatus != status {
				t.Fatalf("provider recovery changed the booking's business status: %q, %v", currentStatus, err)
			}
		})
	}
}
