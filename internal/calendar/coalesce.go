package calendar

import (
	"context"
	"github.com/calnode/calnode/internal/slots"
	"time"
)

type freshKey struct{}

// FreshCheck bypasses shared availability computations at the booking commit
// boundary. Public browsing may share in-flight reads, never stale busy data.
func FreshCheck(ctx context.Context) context.Context { return context.WithValue(ctx, freshKey{}, true) }

type busyCall struct {
	done   chan struct{}
	cancel context.CancelFunc
	refs   int
	busy   []slots.Interval
	err    error
}

func (s *Service) coalescedBusy(ctx context.Context, user string, from, to time.Time) ([]slots.Interval, error) {
	if ctx.Value(freshKey{}) == true {
		return s.freeBusy(ctx, user, from, to)
	}
	key := user + "\x00" + from.UTC().Format(time.RFC3339Nano) + "\x00" + to.UTC().Format(time.RFC3339Nano)
	s.busyMu.Lock()
	if s.busyCalls == nil {
		s.busyCalls = map[string]*busyCall{}
	}
	c := s.busyCalls[key]
	if c == nil {
		work, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		c = &busyCall{done: make(chan struct{}), cancel: cancel}
		s.busyCalls[key] = c
		go func() {
			busy, err := s.freeBusy(work, user, from, to)
			s.busyMu.Lock()
			c.busy = busy
			c.err = err
			if s.busyCalls[key] == c {
				delete(s.busyCalls, key)
			}
			close(c.done)
			s.busyMu.Unlock()
			cancel()
		}()
	}
	c.refs++
	s.busyMu.Unlock()
	defer func() {
		s.busyMu.Lock()
		c.refs--
		if c.refs == 0 {
			c.cancel()
			if s.busyCalls[key] == c {
				delete(s.busyCalls, key)
			}
		}
		s.busyMu.Unlock()
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return append([]slots.Interval(nil), c.busy...), c.err
	}
}
