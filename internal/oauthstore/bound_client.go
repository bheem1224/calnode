package oauthstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/calnode/calnode/internal/providerhttp"
	"github.com/calnode/calnode/internal/secret"
	"golang.org/x/oauth2"
)

type refreshEntry struct {
	lock         chan struct{}
	pending      *oauth2.Token
	expected     string
	failure      error
	failedAccess string
	retryAt      time.Time
}

var refreshEntries sync.Map

type boundSource struct {
	ctx                              context.Context
	db                               *sql.DB
	key                              [32]byte
	provider, user, account, binding string
	cfg                              *oauth2.Config
	entry                            *refreshEntry
}

func BoundClient(ctx context.Context, db *sql.DB, key [32]byte, provider, user, account, binding string, cfg *oauth2.Config, manager *providerhttp.Manager) *http.Client {
	app := providerhttp.Key(provider, cfg.ClientID, cfg.Endpoint.TokenURL)
	mailbox := account
	if mailbox == "" {
		mailbox = user
	}
	scope := providerhttp.Key(app, mailbox)
	credential := providerhttp.Key(fmt.Sprintf("%p", db), provider, user, account, binding, cfg.ClientID, cfg.ClientSecret, cfg.Endpoint.TokenURL)
	raw, _ := refreshEntries.LoadOrStore(credential, &refreshEntry{lock: make(chan struct{}, 1)})
	// Refresh exchanges share the same bounded transport, but execute before the
	// API request acquires a slot. Invalid grants are returned without retrying.
	tokenHTTP := &http.Client{Timeout: 15 * time.Second, Transport: manager.Attach(http.DefaultTransport, app, scope)}
	tokenHTTP.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	ctx = context.WithValue(ctx, oauth2.HTTPClient, tokenHTTP)
	// Do not wrap this source in oauth2.ReuseTokenSource: the database is our
	// shared token cache, and disconnect/reconnect must take effect on an already
	// constructed HTTP client before its next request.
	hc := &http.Client{Transport: &oauth2.Transport{
		Source: &boundSource{ctx, db, key, provider, user, account, binding, cfg, raw.(*refreshEntry)},
		Base:   manager.Attach(http.DefaultTransport, app, scope),
	}}
	hc.Timeout = 20 * time.Second
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return hc
}
func (s *boundSource) Token() (*oauth2.Token, error) {
	select {
	case s.entry.lock <- struct{}{}:
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
	defer func() { <-s.entry.lock }()
	var access, refresh, expiry, binding string
	query := `SELECT access_token_enc,COALESCE(refresh_token_enc,''),COALESCE(expiry_at,''),oauth_provider_id FROM calendar_connections WHERE user_id=? AND provider=? AND account_email=?`
	args := []any{s.user, s.provider, s.account}
	if s.provider == "zoom" {
		query = `SELECT access_token_enc,COALESCE(refresh_token_enc,''),COALESCE(expiry_at,''),'' FROM zoom_connections WHERE user_id=?`
		args = []any{s.user}
	}
	if err := s.db.QueryRowContext(s.ctx, query, args...).Scan(&access, &refresh, &expiry, &binding); err != nil {
		return nil, err
	}
	if binding != s.binding {
		return nil, errors.New("calendar application changed")
	}
	if s.entry.failure != nil && s.entry.failedAccess == access && (s.entry.retryAt.IsZero() || time.Now().Before(s.entry.retryAt)) {
		return nil, s.entry.failure
	}
	s.entry.failure = nil
	persist := func(t *oauth2.Token) error {
		a, err := secret.Encrypt(s.key, t.AccessToken)
		if err != nil {
			return err
		}
		r := ""
		if t.RefreshToken != "" {
			r, err = secret.Encrypt(s.key, t.RefreshToken)
			if err != nil {
				return err
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		query := `UPDATE calendar_connections SET access_token_enc=?,refresh_token_enc=?,expiry_at=? WHERE user_id=? AND provider=? AND account_email=? AND oauth_provider_id=? AND access_token_enc=?`
		args := []any{a, r, t.Expiry.UTC().Format(time.RFC3339Nano), s.user, s.provider, s.account, s.binding, access}
		if s.provider == "zoom" {
			query = `UPDATE zoom_connections SET access_token_enc=?,refresh_token_enc=?,expiry_at=? WHERE user_id=? AND access_token_enc=?`
			args = []any{a, r, t.Expiry.UTC().Format(time.RFC3339Nano), s.user, access}
		}
		result, err := s.db.ExecContext(ctx, query, args...)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return errors.New("calendar connection changed during token refresh")
		}
		return nil
	}
	if p := s.entry.pending; p != nil {
		if access == s.entry.expected {
			if err := persist(p); err != nil {
				return nil, err
			}
			s.entry.pending = nil
			copy := *p
			return &copy, nil
		}
		s.entry.pending = nil // An explicit reconnect wins over an old in-flight refresh.
	}
	a, err := secret.Decrypt(s.key, access)
	if err != nil {
		return nil, err
	}
	r := ""
	if refresh != "" {
		r, err = secret.Decrypt(s.key, refresh)
		if err != nil {
			return nil, err
		}
	}
	at, _ := time.Parse(time.RFC3339Nano, expiry)
	if at.IsZero() {
		at = time.Now().Add(-time.Second)
	}
	old := &oauth2.Token{AccessToken: a, RefreshToken: r, Expiry: at}
	if old.Valid() {
		return old, nil
	}
	token, err := s.cfg.TokenSource(s.ctx, old).Token()
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, err
		}
		until := time.Now().Add(5 * time.Second)
		var rejected *oauth2.RetrieveError
		if errors.As(err, &rejected) {
			if rejected.ErrorCode == "invalid_grant" || rejected.ErrorCode == "invalid_client" || rejected.ErrorCode == "unauthorized_client" {
				err = errors.New("calendar authorization expired, revoked, or misconfigured; reconnect the calendar")
				until = time.Time{}
			} else if rejected.Response != nil {
				err = providerhttp.Error(rejected.Response, "calendar token refresh")
				var deferred interface{ RetryAt() time.Time }
				if errors.As(err, &deferred) && deferred.RetryAt().After(until) {
					until = deferred.RetryAt()
				}
			}
		}
		s.entry.failure = err
		s.entry.failedAccess = access
		s.entry.retryAt = until
		return nil, err
	}
	if token.RefreshToken == "" {
		token.RefreshToken = r
	}
	s.entry.pending = token
	s.entry.expected = access
	if err := persist(token); err != nil {
		return nil, err
	}
	s.entry.pending = nil
	return token, nil
}
