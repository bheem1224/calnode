package calendar

import (
	"context"
	"errors"
	"github.com/calnode/calnode/internal/connstore"
	"github.com/calnode/calnode/internal/db"
	"github.com/calnode/calnode/internal/slots"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type reliabilityProvider struct {
	recordProvider
	calls            atomic.Int32
	started, release chan struct{}
	mu               sync.Mutex
	targets          []string
	fail             bool
}

func (p *reliabilityProvider) FreeBusy(ctx context.Context, _ string, _, _ time.Time) ([]slots.Interval, error) {
	p.calls.Add(1)
	p.started <- struct{}{}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.release:
		return []slots.Interval{}, nil
	}
}
func (p *reliabilityProvider) CreateEvent(_ context.Context, _ string, in CreateEventParams) (string, string, string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.targets = append(p.targets, in.DestinationCalendarID)
	if p.fail {
		p.fail = false
		return "", "", "", errors.New("response lost")
	}
	return "same", "", in.DestinationCalendarID, nil
}
func TestConcurrentAvailabilityIsSharedButCommitCheckIsFresh(t *testing.T) {
	p := &reliabilityProvider{recordProvider: recordProvider{name: "microsoft"}, started: make(chan struct{}, 30), release: make(chan struct{})}
	s := NewService(nil)
	s.Register(p)
	start := time.Now()
	var wg sync.WaitGroup
	first, cancel := context.WithCancel(context.Background())
	defer cancel()
	wg.Add(1)
	go func() { defer wg.Done(); _, _ = s.FreeBusy(first, "user", start, start.Add(time.Hour)) }()
	<-p.started
	for i := 0; i < 15; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := s.FreeBusy(context.Background(), "user", start, start.Add(time.Hour)); e != nil {
				t.Error(e)
			}
		}()
	}
	// Observe all waiters before cancelling the original owner.
	deadline := time.Now().Add(time.Second)
	for {
		s.busyMu.Lock()
		refs := 0
		for _, c := range s.busyCalls {
			refs = c.refs
		}
		s.busyMu.Unlock()
		if refs == 16 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("waiters did not join")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = s.FreeBusy(FreshCheck(context.Background()), "user", start, start.Add(time.Hour))
	}()
	<-p.started
	close(p.release)
	wg.Wait()
	if p.calls.Load() != 2 {
		t.Fatal(p.calls.Load())
	}
}
func TestCreateIntentPinsDestinationAcrossFailureAndRestart(t *testing.T) {
	database, e := db.Open("sqlite://:memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer database.Close()
	if e = db.Migrate(database); e != nil {
		t.Fatal(e)
	}
	_, e = database.Exec(`INSERT INTO users(id,email,name)VALUES('u','u@example.test','User');INSERT INTO calendar_connections(id,user_id,provider,account_email,calendar_id,access_token_enc,is_destination)VALUES('c','u','google','first@example.test','primary','unused',1)`)
	if e != nil {
		t.Fatal(e)
	}
	p := &reliabilityProvider{recordProvider: recordProvider{name: "google"}, fail: true}
	s := NewService(database)
	s.Register(p)
	params := CreateEventParams{IdempotencyKey: "seat-one"}
	if _, _, _, _, e = s.CreateEvent(context.Background(), "u", params); e == nil {
		t.Fatal("failure hidden")
	}
	_, _ = database.Exec(`UPDATE calendar_connections SET account_email='second@example.test'`)
	s = NewService(database)
	s.Register(p)
	if _, _, _, _, e = s.CreateEvent(context.Background(), "u", params); e != nil {
		t.Fatal(e)
	}
	if len(p.targets) != 2 || p.targets[0] != p.targets[1] {
		t.Fatal("retry moved to a different destination", p.targets)
	}
	// An event-specific selection can belong to a different connected account.
	// Pin its owner, rather than combining its calendar ID with the default token.
	_, e = database.Exec(`INSERT INTO calendar_connections(id,user_id,provider,account_email,calendar_id,access_token_enc)VALUES('other','u','google','override@example.test','primary','unused');INSERT INTO connection_calendars(id,user_id,provider,account_email,calendar_id,name)VALUES('picked','u','google','override@example.test','override-calendar','Other calendar')`)
	if e != nil {
		t.Fatal(e)
	}
	params = CreateEventParams{IdempotencyKey: "seat-override", DestinationCalendarID: "override-calendar"}
	if _, _, _, _, e = s.CreateEvent(context.Background(), "u", params); e != nil {
		t.Fatal(e)
	}
	if got := p.targets[len(p.targets)-1]; got != connstore.CalendarReference("google", "override-calendar", "override@example.test") {
		t.Fatal("override pinned to the wrong account", got)
	}
}
