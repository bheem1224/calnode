package abuse_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/abuse"
	"github.com/calnode/calnode/internal/db"
)

func TestSlidingBudgetAtomicRollbackAndExpiry(t *testing.T) {
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err = db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	rule := abuse.Rule{Key: "recipient:private@example.com", Limit: 1, Window: time.Hour}
	var allowed atomic.Int32
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := abuse.Take(ctx, database, rule)
			if err == nil {
				allowed.Add(1)
			} else if !errors.Is(err, abuse.ErrLimited) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if allowed.Load() != 1 {
		t.Fatalf("concurrent reservations=%d", allowed.Load())
	}
	// A rejected second budget must not spend the first one.
	other := abuse.Rule{Key: "other", Limit: 1, Window: time.Hour}
	if err = abuse.Take(ctx, database, other, rule); !errors.Is(err, abuse.ErrLimited) {
		t.Fatal(err)
	}
	if err = abuse.Take(ctx, database, other); err != nil {
		t.Fatal("partial reservation committed", err)
	}
	var key string
	if err = database.QueryRow(`SELECT bucket FROM abuse_events LIMIT 1`).Scan(&key); err != nil || len(key) != 64 {
		t.Fatalf("unsafe counter key=%q %v", key, err)
	}
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = abuse.TakeTx(ctx, tx, time.Now().Add(2*time.Hour), rule); err != nil {
		t.Fatal("expired budget", err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestGateRejectsOverflowAndRecovers(t *testing.T) {
	gate := abuse.NewGate(2)
	if !gate.Enter() || !gate.Enter() || gate.Enter() {
		t.Fatal("concurrency bound failed")
	}
	gate.Leave()
	if !gate.Enter() {
		t.Fatal("permit was not released")
	}
	gate.Leave()
	gate.Leave()
}
