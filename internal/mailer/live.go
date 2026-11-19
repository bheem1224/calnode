package mailer

import (
	"context"
	"database/sql"
	"net/mail"
	"strings"
	"sync"
	"time"

	"github.com/calnode/calnode/internal/abuse"
)

// Live is a Mailer that wraps a hot-swappable inner Mailer. All calls to Send
// are forwarded to whatever the current inner Mailer is at the time of the call,
// so SMTP settings can be changed at runtime without restarting the server.
type Live struct {
	mu       sync.RWMutex
	current  Mailer
	budgetDB *sql.DB
}

var sendGate = abuse.NewGate(abuse.Limit("EMAIL_SEND_CONCURRENCY", 8))

// SetBudgetDB enables durable outbound quotas; set once during startup.
func (l *Live) SetBudgetDB(db *sql.DB) { l.mu.Lock(); defer l.mu.Unlock(); l.budgetDB = db }

// NewLive creates a Live mailer with the given initial Mailer.
func NewLive(initial Mailer) *Live {
	return &Live{current: initial}
}

// Send forwards to the current inner Mailer.
func (l *Live) Send(ctx context.Context, msg Message) error {
	l.mu.RLock()
	m := l.current
	db := l.budgetDB
	l.mu.RUnlock()
	if _, noop := m.(*Noop); noop {
		return nil
	}
	if !sendGate.Enter() {
		return abuse.ErrLimited
	}
	defer sendGate.Leave()
	if db != nil {
		rules := []abuse.Rule{{Key: "email:instance", Cost: len(msg.To), Limit: abuse.Limit("EMAIL_HOURLY_LIMIT", 6000), Window: time.Hour}}
		for _, to := range msg.To {
			if address, err := mail.ParseAddress(to); err == nil {
				to = address.Address
			}
			rules = append(rules, abuse.Rule{Key: "email:recipient:" + strings.ToLower(to), Limit: abuse.Limit("EMAIL_RECIPIENT_HOURLY_LIMIT", 100), Window: time.Hour})
		}
		if err := abuse.Take(ctx, db, rules...); err != nil {
			return err
		}
	}
	return m.Send(ctx, msg)
}

// Swap atomically replaces the inner Mailer.
func (l *Live) Swap(m Mailer) {
	l.mu.Lock()
	l.current = m
	l.mu.Unlock()
}

// IsEnabled reports whether the current inner Mailer is a real sender (not Noop).
func (l *Live) IsEnabled() bool {
	l.mu.RLock()
	_, noop := l.current.(*Noop)
	l.mu.RUnlock()
	return !noop
}
