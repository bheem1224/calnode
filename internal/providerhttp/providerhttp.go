// Package providerhttp coordinates outbound provider traffic. Authentication is
// layered above this transport, so waiting for an OAuth refresh cannot deadlock
// behind the API request that needs it.
package providerhttp

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Config struct {
	AccountConcurrency, AppConcurrency, QueueSize, Attempts int
	AccountInterval, AppInterval, MaxWait                   time.Duration
}

func DefaultConfig() Config {
	return Config{2, 16, 128, 4, 100 * time.Millisecond, 25 * time.Millisecond, 12 * time.Second}
}

type budget struct {
	slots          chan struct{}
	mu             sync.Mutex
	next, cooldown time.Time
}
type Manager struct {
	db      *sql.DB
	cfg     Config
	mu      sync.Mutex
	budgets map[string]*budget
	waiting chan struct{}
}

var Default = New(nil)
var databaseManagers sync.Map

// All configured OAuth apps and recreated clients for one instance share budgets.
func ForDB(db *sql.DB) *Manager {
	if db == nil {
		return Default
	}
	m, _ := databaseManagers.LoadOrStore(db, New(db))
	return m.(*Manager)
}

func New(db *sql.DB) *Manager { return NewWithConfig(db, DefaultConfig()) }
func NewWithConfig(db *sql.DB, cfg Config) *Manager {
	return &Manager{db: db, cfg: cfg, budgets: map[string]*budget{}, waiting: make(chan struct{}, cfg.QueueSize)}
}
func Key(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}
func (m *Manager) get(key string, n int) *budget {
	m.mu.Lock()
	defer m.mu.Unlock()
	if b := m.budgets[key]; b != nil {
		return b
	}
	b := &budget{slots: make(chan struct{}, n)}
	if m.db != nil {
		var raw string
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if m.db.QueryRowContext(ctx, `SELECT until_at FROM provider_backoff WHERE scope=?`, key).Scan(&raw) == nil {
			b.cooldown, _ = time.Parse(time.RFC3339Nano, raw)
		}
	}
	m.budgets[key] = b
	return b
}

type DeferredError struct {
	Until time.Time
	Cause error
}

func (e *DeferredError) Error() string      { return "provider request deferred" }
func (e *DeferredError) Unwrap() error      { return e.Cause }
func (e *DeferredError) RetryAt() time.Time { return e.Until }
func wait(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
func (m *Manager) enter(ctx context.Context, app, account string) (func(), error) {
	select {
	case m.waiting <- struct{}{}:
	default:
		return nil, &DeferredError{Until: time.Now().Add(time.Second), Cause: errors.New("provider queue is full")}
	}
	a, b := m.get(app, m.cfg.AppConcurrency), m.get(account, m.cfg.AccountConcurrency)
	// Reserve the account before the app, so one mailbox cannot fill the app's
	// concurrency slots while all of its callers wait behind that mailbox.
	select {
	case b.slots <- struct{}{}:
	case <-ctx.Done():
		<-m.waiting
		return nil, ctx.Err()
	}
	select {
	case a.slots <- struct{}{}:
	case <-ctx.Done():
		<-b.slots
		<-m.waiting
		return nil, ctx.Err()
	}
	release := func() { <-a.slots; <-b.slots; <-m.waiting }
	for _, p := range []struct {
		b        *budget
		interval time.Duration
	}{{b, m.cfg.AccountInterval}, {a, m.cfg.AppInterval}} {
		p.b.mu.Lock()
		now := time.Now()
		at := p.b.next
		if p.b.cooldown.After(at) {
			at = p.b.cooldown
		}
		if now.After(at) {
			at = now
		}
		p.b.next = at.Add(p.interval)
		p.b.mu.Unlock()
		if deadline, ok := ctx.Deadline(); ok && at.After(deadline) {
			release()
			return nil, &DeferredError{Until: at, Cause: context.DeadlineExceeded}
		}
		if err := wait(ctx, time.Until(at)); err != nil {
			release()
			return nil, err
		}
	}
	// Recheck BOTH scopes after all spacing waits. Another in-flight request may
	// have received a mailbox cooldown while this request waited for the app.
	for {
		var until time.Time
		for _, scope := range []*budget{a, b} {
			scope.mu.Lock()
			if scope.cooldown.After(until) {
				until = scope.cooldown
			}
			scope.mu.Unlock()
		}
		if !until.After(time.Now()) {
			break
		}
		if deadline, ok := ctx.Deadline(); ok && until.After(deadline) {
			release()
			return nil, &DeferredError{Until: until, Cause: context.DeadlineExceeded}
		}
		if err := wait(ctx, time.Until(until)); err != nil {
			release()
			return nil, err
		}
	}
	return release, nil
}
func (m *Manager) cooldown(key string, until time.Time) {
	b := m.get(key, m.cfg.AccountConcurrency)
	b.mu.Lock()
	if until.After(b.cooldown) {
		b.cooldown = until
	}
	b.mu.Unlock()
	if m.db != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, _ = m.db.ExecContext(ctx, `INSERT INTO provider_backoff(scope,until_at)VALUES(?,?) ON CONFLICT(scope)DO UPDATE SET until_at=MAX(until_at,excluded.until_at)`, key, until.UTC().Format("2006-01-02T15:04:05.000000000Z"))
	}
}
func RetryAfter(raw string, now time.Time) time.Time {
	if n, e := strconv.ParseInt(strings.TrimSpace(raw), 10, 32); e == nil && n >= 0 {
		return now.Add(time.Duration(n) * time.Second)
	}
	if at, e := http.ParseTime(raw); e == nil && at.After(now) {
		return at
	}
	return time.Time{}
}

type HTTPError struct {
	Operation string
	Status    int
	Until     time.Time
}

func (e *HTTPError) Error() string      { return fmt.Sprintf("%s status %d", e.Operation, e.Status) }
func (e *HTTPError) RetryAt() time.Time { return e.Until }
func Error(resp *http.Response, operation string) error {
	return &HTTPError{operation, resp.StatusCode, RetryAfter(resp.Header.Get("Retry-After"), time.Now())}
}

type replayKey struct{}
type beforeSendKey struct{}

// BeforeSend records a durable write intent after authorization and queueing,
// immediately before the transport may send bytes to the provider.
func BeforeSend(r *http.Request, fn func(context.Context) error) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), beforeSendKey{}, fn))
}

// ReplaySafe is for read-only POSTs and creates with provider-enforced stable IDs.
// PATCH is deliberately excluded: idempotent state changes may still email twice.
func ReplaySafe(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), replayKey{}, true))
}

type Transport struct {
	Base         http.RoundTripper
	Manager      *Manager
	App, Account string
}

func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(req.Context(), t.Manager.cfg.MaxWait)
	// The response body owns cancellation on a successful return.
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	safe := req.Method == "GET" || req.Method == "HEAD" || req.Method == "DELETE" || req.Context().Value(replayKey{}) == true
	for attempt := 0; attempt < t.Manager.cfg.Attempts; attempt++ {
		release, err := t.Manager.enter(ctx, t.App, t.Account)
		if err != nil {
			cancel()
			return nil, err
		}
		r := req.Clone(ctx)
		if attempt > 0 && req.Body != nil {
			if req.GetBody == nil {
				release()
				cancel()
				return nil, errors.New("provider body cannot be replayed")
			}
			r.Body, err = req.GetBody()
			if err != nil {
				release()
				cancel()
				return nil, err
			}
		}
		if fn, ok := r.Context().Value(beforeSendKey{}).(func(context.Context) error); ok {
			if err := fn(ctx); err != nil {
				release()
				cancel()
				return nil, err
			}
		}
		resp, err := base.RoundTrip(r)
		retry := safe && (err != nil && ctx.Err() == nil)
		if resp != nil {
			throttled := resp.StatusCode == 429
			if resp.StatusCode == 403 {
				raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
				_ = resp.Body.Close()
				resp.Body = io.NopCloser(strings.NewReader(string(raw)))
				var data struct {
					Error struct {
						Errors []struct {
							Reason string `json:"reason"`
						} `json:"errors"`
					} `json:"error"`
				}
				_ = json.Unmarshal(raw, &data)
				for _, e := range data.Error.Errors {
					if e.Reason == "rateLimitExceeded" || e.Reason == "userRateLimitExceeded" {
						throttled = true
					}
				}
			}
			if throttled || (resp.StatusCode == 503 && resp.Header.Get("Retry-After") != "") {
				until := RetryAfter(resp.Header.Get("Retry-After"), time.Now())
				if until.IsZero() {
					until = time.Now().Add(time.Second << attempt)
				}
				t.Manager.cooldown(t.Account, until)
				if strings.Contains(strings.ToLower(resp.Header.Get("x-ms-throttle-scope")), "application") {
					t.Manager.cooldown(t.App, until)
				}
			}
			retry = safe && (throttled || resp.StatusCode == 408 || resp.StatusCode == 500 || resp.StatusCode == 502 || resp.StatusCode == 503 || resp.StatusCode == 504)
		}
		if !retry || attempt+1 == t.Manager.cfg.Attempts {
			if err != nil {
				release()
				cancel()
				return resp, err
			}
			resp.Body = &bodyCloser{ReadCloser: resp.Body, cancel: func() { release(); cancel() }}
			return resp, nil
		}
		until := time.Time{}
		if resp != nil {
			until = RetryAfter(resp.Header.Get("Retry-After"), time.Now())
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
			_ = resp.Body.Close()
		}
		release()
		delay := time.Duration(float64(250*time.Millisecond*time.Duration(1<<attempt)) * (0.8 + rand.Float64()*0.4))
		if time.Until(until) > delay {
			delay = time.Until(until)
		}
		if deadline, ok := ctx.Deadline(); ok && time.Now().Add(delay).After(deadline) {
			cancel()
			return nil, &DeferredError{Until: until, Cause: context.DeadlineExceeded}
		}
		if err := wait(ctx, delay); err != nil {
			cancel()
			return nil, err
		}
	}
	cancel()
	return nil, errors.New("provider retry budget exhausted")
}

type bodyCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
	once   sync.Once
}

func (b *bodyCloser) Close() error { err := b.ReadCloser.Close(); b.once.Do(b.cancel); return err }

// Attach preserves the authenticated transport and its shared connection pool.
// It is called on the Base of oauth2.Transport, below token acquisition.
func (m *Manager) Attach(base http.RoundTripper, app, account string) http.RoundTripper {
	return &Transport{Base: base, Manager: m, App: app, Account: account}
}
