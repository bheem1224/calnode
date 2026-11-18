// Package authsecurity contains recovery operations shared by HTTP and the local emergency command.
package authsecurity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/calnode/calnode/internal/uid"
	"time"
)

func Token() (string, error) {
	var b [32]byte
	_, err := rand.Read(b[:])
	return hex.EncodeToString(b[:]), err
}
func Hash(s string) string { v := sha256.Sum256([]byte(s)); return hex.EncodeToString(v[:]) }

// Recover invalidates credentials before issuing a setup-only, expiring capability.
// Authorization and operator identity verification must occur before this call.
func Recover(ctx context.Context, db *sql.DB, target, actor, reason string, allowOwner, disable bool) (string, error) {
	raw, err := Token()
	if err != nil {
		return "", err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback() //nolint:errcheck
	var owner, admin bool
	if err = tx.QueryRowContext(ctx, `SELECT is_owner,is_admin FROM users WHERE id=? AND archived_at IS NULL`, target).Scan(&owner, &admin); err != nil {
		return "", err
	}
	if owner && !allowOwner {
		return "", fmt.Errorf("owner recovery requires the server recovery command")
	}
	if admin && !allowOwner {
		var privileged bool
		if err = tx.QueryRowContext(ctx, `SELECT (is_admin=1 OR is_owner=1) FROM users WHERE id=? AND archived_at IS NULL`, actor).Scan(&privileged); err != nil || !privileged {
			return "", fmt.Errorf("administrator recovery requires an instance administrator")
		}
	}
	for _, table := range []string{"user_totp", "passkeys", "auth_recovery_codes", "auth_challenges", "sessions", "oauth_access_tokens", "oauth_auth_codes", "magic_link_tokens", "password_reset_tokens"} { // Tables are fixed code-owned names, never user input.
		query := `DELETE FROM ` + table + ` WHERE user_id=?`
		args := []any{target}
		if table == "sessions" {
			query += ` OR impersonator_user_id=?`
			args = append(args, target)
		}
		if _, err = tx.ExecContext(ctx, query, args...); err != nil {
			return "", err
		}
	}
	if admin || owner {
		if _, err = tx.ExecContext(ctx, `DELETE FROM api_keys WHERE user_id=?`, target); err != nil {
			return "", err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE users SET mfa_reenroll_required=? WHERE id=?`, !disable, target); err != nil {
		return "", err
	}
	payload, _ := json.Marshal(map[string]any{"actor": actor, "reason": reason, "owner_recovery": allowOwner})
	if !disable {
		_, err = tx.ExecContext(ctx, `INSERT INTO auth_challenges(token_hash,user_id,purpose,payload,expires_at)VALUES(?,?,'recovery',?,?)`, Hash(raw), target, string(payload), time.Now().UTC().Add(15*time.Minute).Format(time.RFC3339Nano))
		if err != nil {
			return "", err
		}
	}
	audit, _ := json.Marshal(map[string]any{"reason": reason, "disable": disable, "emergency": allowOwner})
	if _, err = tx.ExecContext(ctx, `INSERT INTO security_audit_logs(id,actor_user_id,action,target_id,details)VALUES(?,?,'auth.mfa_recovery',?,?)`, uid.New(), actor, target, string(audit)); err != nil {
		return "", err
	}
	notice, _ := json.Marshal(map[string]string{"user_id": target, "action": "Authentication recovery was authorized", "reason": reason})
	if _, err = tx.ExecContext(ctx, `INSERT INTO jobs(id,type,payload,run_at)VALUES(?,'security.notify',?,?)`, uid.New(), string(notice), time.Now().UTC().Format(time.RFC3339)); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	if disable {
		return "", nil
	}
	return raw, nil
}
