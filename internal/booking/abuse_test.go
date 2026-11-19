package booking_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/abuse"
	"github.com/calnode/calnode/internal/booking"
)

func TestGuestSettingAndRecipientBudgetAtWriteBoundary(t *testing.T) {
	database := newTestDB(t)
	host := seedHost(t, database)
	event := seedEventType(t, database, host)
	svc := booking.New(database)
	params := booking.CreateParams{EventTypeID: event, HostIDs: []string{host}, StartAt: slot(9, 0), EndAt: slot(9, 30), Organizer: booking.Attendee{Name: "Alice", Email: "alice@example.com"}, Guests: []string{"guest@example.com"}}
	if _, err := database.Exec(`UPDATE event_types SET booking_form_config='[{"key":"guests","hidden":true}]' WHERE id=?`, event); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(context.Background(), params); !errors.Is(err, booking.ErrInvalidGuests) {
		t.Fatalf("hidden guest accepted: %v", err)
	}
	params.Guests = nil
	if _, err := svc.Create(context.Background(), params); err != nil {
		t.Fatal("organizer-only booking failed", err)
	}
	if _, err := database.Exec(`UPDATE event_types SET booking_form_config='' WHERE id=?`, event); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BOOKING_RECIPIENT_HOURLY_LIMIT", "1")
	params.Guests = []string{"guest@example.com"}
	params.StartAt = slot(10, 0)
	params.EndAt = slot(10, 30)
	params.Organizer.Email = "different@example.com"
	if _, err := svc.Create(context.Background(), params); err != nil {
		t.Fatal(err)
	}
	params.StartAt = slot(11, 0)
	params.EndAt = slot(11, 30)
	params.Organizer.Email = "rotated@example.com"
	if _, err := svc.Create(context.Background(), params); !errors.Is(err, abuse.ErrLimited) {
		t.Fatalf("rotating organizer bypassed recipient limit: %v", err)
	}
	// Failed creation did not consume event/instance budgets or create a booking.
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM bookings`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("failed booking persisted: %d %v", count, err)
	}
}

func TestInstanceBudgetAtomicAcrossEventsAndIdentities(t *testing.T) {
	database := newTestDB(t)
	svc := booking.New(database)
	t.Setenv("BOOKING_HOURLY_LIMIT", "1")
	var success atomic.Int32
	var wg sync.WaitGroup
	for i := range 8 {
		host := seedHost(t, database)
		event := seedEventType(t, database, host)
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.Create(context.Background(), booking.CreateParams{EventTypeID: event, HostIDs: []string{host}, StartAt: slot(9, 0), EndAt: slot(9, 0).Add(30 * time.Minute), Organizer: booking.Attendee{Name: "Person", Email: fmt.Sprintf("person%d@example.com", i)}})
			if err == nil {
				success.Add(1)
			} else if !errors.Is(err, abuse.ErrLimited) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if success.Load() != 1 {
		t.Fatalf("instance budget admitted %d", success.Load())
	}
}
