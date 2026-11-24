package providerhttp

import (
	"context"
	"errors"
	"github.com/calnode/calnode/internal/db"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCooldownPersistsAcrossRestart(t *testing.T) {
	database, e := db.Open("sqlite://:memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer database.Close()
	if e = db.Migrate(database); e != nil {
		t.Fatal(e)
	}
	first := New(database)
	until := time.Now().Add(time.Hour)
	first.cooldown("mailbox", until)
	second := New(database)
	hc := http.Client{Transport: second.Attach(roundTripper(func(*http.Request) (*http.Response, error) {
		t.Error("request escaped saved cooldown")
		return response(200, ""), nil
	}), "app", "mailbox")}
	_, e = hc.Get("https://provider.test")
	var deferred *DeferredError
	if !errors.As(e, &deferred) || deferred.Until.Before(until.Add(-time.Millisecond)) {
		t.Fatal(e)
	}
	if ForDB(database) != ForDB(database) {
		t.Fatal("recreated clients do not share their instance limiter")
	}
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func testManager() *Manager {
	c := DefaultConfig()
	c.AccountInterval = 0
	c.AppInterval = 0
	return NewWithConfig(nil, c)
}
func response(code int, after string) *http.Response {
	h := http.Header{}
	if after != "" {
		h.Set("Retry-After", after)
	}
	return &http.Response{StatusCode: code, Header: h, Body: io.NopCloser(strings.NewReader(`{}`))}
}
func TestRetryPolicyDoesNotReplayUnsafeWrites(t *testing.T) {
	for _, method := range []string{"POST", "PATCH"} {
		for _, code := range []int{400, 401, 403, 429, 503} {
			calls := 0
			m := testManager()
			hc := http.Client{Transport: m.Attach(roundTripper(func(*http.Request) (*http.Response, error) { calls++; return response(code, "0"), nil }), "app", "account")}
			req, _ := http.NewRequest(method, "https://provider.test/events", strings.NewReader(`{"event":"same"}`))
			r, e := hc.Do(req)
			if e != nil {
				t.Fatal(e)
			}
			r.Body.Close()
			if calls != 1 {
				t.Fatalf("%s %d replayed %d times", method, code, calls)
			}
		}
	}
}
func TestSafeRetriesReplayIdenticalBodyAndStop(t *testing.T) {
	m := testManager()
	calls := 0
	hc := http.Client{Transport: m.Attach(roundTripper(func(r *http.Request) (*http.Response, error) {
		calls++
		b, _ := io.ReadAll(r.Body)
		if string(b) != `{"id":"stable"}` {
			t.Fatalf("changed request %s", b)
		}
		if calls < 3 {
			return response(503, "0"), nil
		}
		return response(200, ""), nil
	}), "app", "account")}
	req, _ := http.NewRequest("POST", "https://provider.test/events", strings.NewReader(`{"id":"stable"}`))
	r, e := hc.Do(ReplaySafe(req))
	if e != nil {
		t.Fatal(e)
	}
	r.Body.Close()
	if calls != 3 {
		t.Fatal(calls)
	}
}
func TestAccountConcurrencyAndCancellationReleaseSlots(t *testing.T) {
	m := testManager()
	var active, max atomic.Int32
	entered := make(chan struct{}, 8)
	release := make(chan struct{})
	hc := http.Client{Transport: m.Attach(roundTripper(func(*http.Request) (*http.Response, error) {
		n := active.Add(1)
		for {
			old := max.Load()
			if n <= old || max.CompareAndSwap(old, n) {
				break
			}
		}
		entered <- struct{}{}
		<-release
		active.Add(-1)
		return response(200, ""), nil
	}), "app", "account")}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := hc.Get("https://provider.test")
			if e != nil {
				t.Error(e)
				return
			}
			r.Body.Close()
		}()
	}
	<-entered
	<-entered
	select {
	case <-entered:
		t.Fatal("more than two concurrent requests")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	wg.Wait()
	if max.Load() > 2 {
		t.Fatal(max.Load())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://provider.test", nil)
	_, e := hc.Do(req)
	if !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
func TestCooldownSharedAcrossClientsAndQueueBounded(t *testing.T) {
	m := testManager()
	m.cooldown("account", time.Now().Add(time.Hour))
	var calls atomic.Int32
	hc := http.Client{Transport: m.Attach(roundTripper(func(*http.Request) (*http.Response, error) { calls.Add(1); return response(200, ""), nil }), "app", "account")}
	_, err := hc.Get("https://provider.test")
	var d *DeferredError
	if !errors.As(err, &d) || d.RetryAt().Before(time.Now().Add(59*time.Minute)) || calls.Load() != 0 {
		t.Fatalf("cooldown ignored: %v %d", err, calls.Load())
	}
	// Other mailboxes are unaffected by an account-scoped cooldown.
	other := http.Client{Transport: m.Attach(roundTripper(func(*http.Request) (*http.Response, error) { return response(200, ""), nil }), "app", "other")}
	r, err := other.Get("https://provider.test")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	for i := 0; i < cap(m.waiting); i++ {
		m.waiting <- struct{}{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err = m.enter(ctx, "app", "other"); !errors.As(err, &d) {
		t.Fatal("unbounded queue", err)
	}
}
func TestRetryAfterDateAndGoogleQuota(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	if RetryAfter("3", now) != now.Add(3*time.Second) || !RetryAfter(now.Add(time.Minute).Format(http.TimeFormat), now).Equal(now.Add(time.Minute)) {
		t.Fatal("Retry-After parsing")
	}
	m := testManager()
	calls := 0
	hc := http.Client{Transport: m.Attach(roundTripper(func(*http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			r := response(403, "0")
			r.Body = io.NopCloser(strings.NewReader(`{"error":{"errors":[{"reason":"userRateLimitExceeded"}]}}`))
			return r, nil
		}
		return response(200, ""), nil
	}), "app", "account")}
	r, e := hc.Get("https://provider.test")
	if e != nil {
		t.Fatal(e)
	}
	r.Body.Close()
	if calls != 2 {
		t.Fatal(calls)
	}
}

func TestAccountConcurrencySlotHeldUntilBodyClosed(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AccountConcurrency = 1
	cfg.AccountInterval, cfg.AppInterval = 0, 0
	m := NewWithConfig(nil, cfg)
	var calls atomic.Int32
	hc := &http.Client{Transport: m.Attach(roundTripper(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return response(200, ""), nil
	}), "app", "account")}
	first, err := hc.Get("https://provider.test")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Body.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://provider.test", nil)
	if _, err := hc.Do(req); !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 1 {
		t.Fatal("open response released account concurrency early", err, calls.Load())
	}
	first.Body.Close()
	last, err := hc.Get("https://provider.test")
	if err != nil {
		t.Fatal("cancelled waiter leaked slot", err)
	}
	last.Body.Close()
}

func TestAccountConcurrencyAlsoEnforcesSharedAppBudget(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AccountConcurrency, cfg.AppConcurrency = 1, 2
	cfg.AccountInterval, cfg.AppInterval = 0, 0
	m := NewWithConfig(nil, cfg)
	var active atomic.Int32
	started := make(chan struct{}, 8)
	finish := make(chan struct{})
	base := roundTripper(func(*http.Request) (*http.Response, error) {
		if active.Add(1) > 2 {
			t.Error("application concurrency exceeded across mailboxes")
		}
		started <- struct{}{}
		<-finish
		active.Add(-1)
		return response(200, ""), nil
	})
	var wg sync.WaitGroup
	for _, account := range []string{"a", "b", "c", "d", "e", "f"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			hc := &http.Client{Transport: m.Attach(base, "same-app", account)}
			r, err := hc.Get("https://provider.test")
			if err != nil {
				t.Error(err)
				return
			}
			r.Body.Close()
		}()
	}
	<-started
	<-started
	select {
	case <-started:
		t.Error("more than two app requests in flight")
	case <-time.After(30 * time.Millisecond):
	}
	close(finish)
	wg.Wait()
}
