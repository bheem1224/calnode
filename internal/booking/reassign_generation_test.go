package booking_test

import (
	"context"
	"testing"

	"github.com/calnode/calnode/internal/booking"
)

func TestReassignUsesNewCalendarOperationForEachAssignment(t *testing.T) {
	database := newTestDB(t)
	svc := booking.New(database)
	a, b := seedHost(t, database), seedHost(t, database)
	event := seedEventType(t, database, a)
	ctx := context.Background()
	created, err := svc.Create(ctx, booking.CreateParams{EventTypeID: event, HostIDs: []string{a}, StartAt: slot(9, 0), EndAt: slot(9, 30), Organizer: booking.Attendee{Name: "Client", Email: "client@example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, host := range []string{a, b, a} {
		if _, err = svc.ReassignHost(ctx, created.ID, host); err != nil {
			t.Fatal(err)
		}
		var assignment string
		if err = database.QueryRow(`SELECT id FROM booking_hosts WHERE booking_id=? AND is_primary=1`, created.ID).Scan(&assignment); err != nil {
			t.Fatal(err)
		}
		if seen[assignment] || assignment == "" {
			t.Fatal("returning to a previous host reused the cancelled calendar operation")
		}
		seen[assignment] = true
		// Repeating the SAME reassignment is a no-op, preserving retry identity.
		if _, err = svc.ReassignHost(ctx, created.ID, host); err != nil {
			t.Fatal(err)
		}
		var retry string
		if err = database.QueryRow(`SELECT id FROM booking_hosts WHERE booking_id=? AND is_primary=1`, created.ID).Scan(&retry); err != nil || retry != assignment {
			t.Fatal("retry changed calendar operation", err)
		}
	}
}
