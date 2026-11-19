package handler

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/calnode/calnode/internal/abuse"
)

func (h *Handler) takeBudget(w http.ResponseWriter, r *http.Request, rules ...abuse.Rule) bool {
	err := abuse.Take(r.Context(), h.db, rules...)
	if err == nil {
		return true
	}
	if errors.Is(err, abuse.ErrLimited) {
		h.logger.WarnContext(r.Context(), "operation throttled", "operation", r.Pattern)
		w.Header().Set("Retry-After", "60")
		h.writeError(w, 429, abuse.ErrLimited.Error())
	} else {
		h.logger.ErrorContext(r.Context(), "abuse budget unavailable", "error", err)
		h.writeError(w, 503, "temporarily unavailable")
	}
	return false
}

// Reserve before bcrypt so parallel guesses spend the same account budget. Delays
// start after five attempts and grow to five minutes, expiring after 15 minutes.
// Unknown accounts take the identical path. No sleeping request goroutines or
// permanent lockouts; passkeys, SSO and password recovery remain available.
func (h *Handler) reservePasswordAttempt(ctx context.Context, email string) error {
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UnixMilli()
	if _, err = tx.ExecContext(ctx, `DELETE FROM login_backoff WHERE expires_at<=?`, now); err != nil {
		return err
	}
	key := abuse.Hash(email)
	var failures int
	var retry int64
	err = tx.QueryRowContext(ctx, `SELECT failures,retry_at FROM login_backoff WHERE account_hash=?`, key).Scan(&failures, &retry)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if retry > now {
		return abuse.ErrLimited
	}
	failures++
	delay := time.Duration(0)
	if failures >= 5 {
		delay = time.Second * time.Duration(1<<min(failures-5, 9))
		if delay > 5*time.Minute {
			delay = 5 * time.Minute
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO login_backoff(account_hash,failures,retry_at,expires_at) VALUES(?,?,?,?) ON CONFLICT(account_hash) DO UPDATE SET failures=excluded.failures,retry_at=excluded.retry_at,expires_at=excluded.expires_at`, key, min(failures, 15), now+delay.Milliseconds(), now+(15*time.Minute).Milliseconds())
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (h *Handler) allowAuthEmail(w http.ResponseWriter, r *http.Request, email string) bool {
	return h.takeBudget(w, r,
		abuse.Rule{Key: "auth-email:cooldown:" + email, Limit: 1, Window: time.Minute},
		abuse.Rule{Key: "auth-email:recipient:" + email, Limit: 5, Window: time.Hour},
		abuse.Rule{Key: "auth-email:instance", Limit: abuse.Limit("AUTH_EMAIL_HOURLY_LIMIT", 300), Window: time.Hour})
}
