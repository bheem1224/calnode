package calendar

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/db"
)

type cleanupProvider struct {
	recordProvider
	mu                     sync.Mutex
	events                 map[string]string
	deleted                []string
	failCreate, failDelete bool
	started, release       chan struct{}
}

func (p *cleanupProvider) Connected(context.Context, string) (bool, error) { return true, nil }
func (p *cleanupProvider) CreateEvent(ctx context.Context, user string, in CreateEventParams) (string, string, string, error) {
	if p.started != nil {
		p.started <- struct{}{}
		select {
		case <-ctx.Done():
			return "", "", "", ctx.Err()
		case <-p.release:
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	id := user + ":" + in.IdempotencyKey
	p.events[id] = in.DestinationCalendarID
	if p.failCreate {
		return "", "", "", errors.New("all create acknowledgements lost")
	}
	return id, "", in.DestinationCalendarID, nil
}
func (p *cleanupProvider) RecoverEvent(_ context.Context, user, reference, key string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	id := user + ":" + key
	if target, ok := p.events[id]; ok {
		if target != reference {
			return "", errors.New("wrong recovery target")
		}
		return id, nil
	}
	return "", nil
}
func (p *cleanupProvider) CancelEvent(_ context.Context, user, reference, id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failDelete {
		return errors.New("calendar outage")
	}
	if target, ok := p.events[id]; ok {
		if target != reference {
			return errors.New("wrong cleanup target")
		}
		delete(p.events, id)
		p.deleted = append(p.deleted, id)
	}
	return nil
}

func cleanupFixture(t *testing.T) (*sql.DB, *Service, *cleanupProvider, CreateEventParams) {
	t.Helper()
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err = db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(`INSERT INTO users(id,email,name)VALUES('a','a@example.test','A'),('b','b@example.test','B');INSERT INTO event_types(id,user_id,slug,name,duration_minutes)VALUES('et','a','meeting','Meeting',30);INSERT INTO bookings(id,event_type_id,host_id,start_at,end_at,status)VALUES('bk','et','a','2099-01-01T10:00:00Z','2099-01-01T10:30:00Z','confirmed');INSERT INTO booking_hosts(id,booking_id,user_id,is_primary)VALUES('seat-a','bk','a',1);INSERT INTO calendar_connections(id,user_id,provider,account_email,calendar_id,access_token_enc,is_destination)VALUES('ca','a','google','a@example.test','primary','unused',1),('cb','b','google','b@example.test','primary','unused',1)`)
	if err != nil {
		t.Fatal(err)
	}
	p := &cleanupProvider{recordProvider: recordProvider{name: "google"}, events: map[string]string{}}
	s := NewService(database)
	s.Register(p)
	return database, s, p, CreateEventParams{BookingID: "bk", AssignmentID: "seat-a", IdempotencyKey: "booking-host:seat-a", Start: time.Date(2099, 1, 1, 10, 0, 0, 0, time.UTC), End: time.Date(2099, 1, 1, 10, 30, 0, 0, time.UTC)}
}

func TestDurableCleanupRecoversUnacknowledgedCreateAfterCancellationAndRestart(t *testing.T) {
	database, s, p, in := cleanupFixture(t)
	p.failCreate = true
	if _, _, _, _, err := s.CreateEvent(context.Background(), "a", in); err == nil {
		t.Fatal("lost response hidden")
	}
	if _, err := database.Exec(`UPDATE bookings SET status='cancelled' WHERE id='bk'`); err != nil {
		t.Fatal(err)
	}
	p.failDelete = true
	if err := s.ReconcileCleanup(context.Background()); err == nil {
		t.Fatal("outage hidden")
	}
	var pending int
	if err := database.QueryRow(`SELECT COUNT(*) FROM calendar_event_cleanup WHERE done=0 AND event_id<>''`).Scan(&pending); err != nil || pending != 1 {
		t.Fatal("recovered ID not retained", pending, err)
	}
	// Booking destruction must not cascade away required provider cleanup.
	if _, err := database.Exec(`DELETE FROM bookings WHERE id='bk';UPDATE calendar_event_cleanup SET next_attempt_at=''`); err != nil {
		t.Fatal(err)
	}
	s = NewService(database)
	s.Register(p)
	p.failDelete = false
	if err := s.ReconcileCleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileCleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.events) != 0 || len(p.deleted) != 1 {
		t.Fatal("orphan or repeated cancellation", p.events, p.deleted)
	}
}

func TestDurableCleanupWaitsForInflightCreateAndRejectsRetiredAssignment(t *testing.T) {
	database, s, p, in := cleanupFixture(t)
	p.started = make(chan struct{}, 1)
	p.release = make(chan struct{})
	created := make(chan error, 1)
	go func() { _, _, _, _, err := s.CreateEvent(context.Background(), "a", in); created <- err }()
	<-p.started
	if _, err := database.Exec(`UPDATE bookings SET status='cancelled' WHERE id='bk'`); err != nil {
		t.Fatal(err)
	}
	cleaned := make(chan error, 1)
	go func() { cleaned <- s.ReconcileCleanup(context.Background()) }()
	close(p.release)
	if err := <-created; !errors.Is(err, ErrObsoleteAssignment) {
		t.Fatal("obsolete create reported success", err)
	}
	if err := <-cleaned; err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := s.CreateEvent(context.Background(), "a", in); !errors.Is(err, ErrObsoleteAssignment) {
		t.Fatal("retired operation recreated", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.events) != 0 || len(p.deleted) != 1 {
		t.Fatal("late event was not cleaned once", p.events, p.deleted)
	}
}

func TestDurableCleanupPreservesReassignmentTargetAcrossOutage(t *testing.T) {
	database, s, p, in := cleanupFixture(t)
	if _, _, _, _, err := s.CreateEvent(context.Background(), "a", in); err != nil {
		t.Fatal(err)
	}
	if _, changed, err := booking.New(database).ReassignHostWithChange(context.Background(), "bk", "b"); err != nil || !changed {
		t.Fatal(err)
	}
	var next string
	if err := database.QueryRow(`SELECT id FROM booking_hosts WHERE booking_id='bk' AND is_primary=1`).Scan(&next); err != nil {
		t.Fatal(err)
	}
	newParams := in
	newParams.AssignmentID = next
	newParams.IdempotencyKey = "booking-host:" + next
	if _, _, _, _, err := s.CreateEvent(context.Background(), "b", newParams); err != nil {
		t.Fatal(err)
	}
	p.failDelete = true
	if err := s.ReconcileCleanup(context.Background()); err == nil {
		t.Fatal("outage hidden")
	}
	if _, _, _, _, err := s.CreateEvent(context.Background(), "a", in); !errors.Is(err, ErrObsoleteAssignment) {
		t.Fatal("former host operation remained live", err)
	}
	if _, err := database.Exec(`UPDATE calendar_event_cleanup SET next_attempt_at=''`); err != nil {
		t.Fatal(err)
	}
	p.failDelete = false
	s = NewService(database)
	s.Register(p)
	if err := s.ReconcileCleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.events) != 1 || p.events["b:"+newParams.IdempotencyKey] == "" || len(p.deleted) != 1 {
		t.Fatal("wrong assignment cleaned", p.events, p.deleted)
	}
}

func TestDurableCleanupDoesNotTreatOneMissingReadAsNoWrite(t *testing.T) {
	database, s, p, in := cleanupFixture(t)
	p.failCreate = true
	_, _, _, _, _ = s.CreateEvent(context.Background(), "a", in)
	p.mu.Lock()
	target := p.events["a:"+in.IdempotencyKey]
	delete(p.events, "a:"+in.IdempotencyKey)
	p.mu.Unlock()
	if _, err := database.Exec(`UPDATE bookings SET status='cancelled' WHERE id='bk'`); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileCleanup(context.Background()); err == nil {
		t.Fatal("one absent lookup finalized uncertain work")
	}
	var done int
	if err := database.QueryRow(`SELECT done FROM calendar_event_cleanup`).Scan(&done); err != nil || done != 0 {
		t.Fatal("uncertain target forgotten", err)
	}
	// The provider's accepted write becomes visible after the first read.
	p.mu.Lock()
	p.events["a:"+in.IdempotencyKey] = target
	p.mu.Unlock()
	if _, err := database.Exec(`UPDATE calendar_event_cleanup SET next_attempt_at=''`); err != nil {
		t.Fatal(err)
	}
	s = NewService(database)
	s.Register(p)
	if err := s.ReconcileCleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.events) != 0 || len(p.deleted) != 1 {
		t.Fatal("late provider visibility left an orphan", p.events)
	}
}

func TestDurableCleanupKeepsTimeDriftWhenRescheduleOverlapsCreation(t *testing.T) {
	database, s, p, in := cleanupFixture(t)
	p.started = make(chan struct{}, 1)
	p.release = make(chan struct{})
	finished := make(chan error, 1)
	go func() { _, _, _, _, err := s.CreateEvent(context.Background(), "a", in); finished <- err }()
	<-p.started
	if _, err := database.Exec(`UPDATE bookings SET start_at='2099-01-01T11:00:00Z',end_at='2099-01-01T11:30:00Z' WHERE id='bk'`); err != nil {
		t.Fatal(err)
	}
	close(p.release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	var dirty int
	if err := database.QueryRow(`SELECT needs_sync FROM booking_hosts WHERE id='seat-a'`).Scan(&dirty); err != nil || dirty != 1 {
		t.Fatal("pending creation hid a reschedule", dirty, err)
	}
}
