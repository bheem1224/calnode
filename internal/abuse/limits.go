// Package abuse provides durable, atomic sliding-window budgets. Keys are hashed
// so counters do not retain email addresses. Budgets are independent of client IP.
package abuse

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"strconv"
	"time"
)

var ErrLimited = errors.New("temporarily busy; please try again later")

type Rule struct {
	Key         string
	Limit, Cost int
	Window      time.Duration
}

func Hash(key string) string { s := sha256.Sum256([]byte(key)); return hex.EncodeToString(s[:]) }

// Limit accepts only positive overrides; security budgets cannot be disabled.
func Limit(name string, fallback int) int {
	n, err := strconv.Atoi(os.Getenv(name))
	if err == nil && n > 0 && n <= 1000000 {
		return n
	}
	return fallback
}

func Take(ctx context.Context, db *sql.DB, rules ...Rule) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = TakeTx(ctx, tx, time.Now(), rules...); err != nil {
		return err
	}
	return tx.Commit()
}

// TakeTx reserves all budgets in the caller's transaction. A denied or failed
// operation rolls them all back; concurrent requests cannot both spend the last unit.
func TakeTx(ctx context.Context, tx *sql.Tx, now time.Time, rules ...Rule) error {
	stamp := now.UnixMilli()
	if _, err := tx.ExecContext(ctx, `DELETE FROM abuse_events WHERE expires_at<=?`, stamp); err != nil {
		return err
	}
	for _, rule := range rules {
		if rule.Cost <= 0 {
			rule.Cost = 1
		}
		key := Hash(rule.Key)
		var used int
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(cost),0) FROM abuse_events WHERE bucket=? AND occurred_at>?`, key, now.Add(-rule.Window).UnixMilli()).Scan(&used); err != nil {
			return err
		}
		if rule.Limit <= 0 || rule.Window <= 0 || used+rule.Cost > rule.Limit {
			return ErrLimited
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO abuse_events(bucket,cost,occurred_at,expires_at) VALUES(?,?,?,?)`, key, rule.Cost, stamp, now.Add(rule.Window).UnixMilli()); err != nil {
			return err
		}
	}
	return nil
}

// CleanupOAuthClients expires abandoned registrations without deleting usable
// grants (refresh tokens may legitimately outlive their access-token expiry).
func CleanupOAuthClients(ctx context.Context, tx *sql.Tx, now time.Time) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM oauth_clients WHERE created_at<? AND NOT EXISTS(SELECT 1 FROM oauth_access_tokens t WHERE t.client_id=oauth_clients.client_id) AND NOT EXISTS(SELECT 1 FROM oauth_auth_codes c WHERE c.client_id=oauth_clients.client_id AND c.expires_at>?)`, now.Add(-24*time.Hour).UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339))
	return err
}
