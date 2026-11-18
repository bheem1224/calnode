-- +goose Up
CREATE TABLE dynamic_links (
 token_id TEXT PRIMARY KEY REFERENCES single_use_tokens(id) ON DELETE CASCADE,
 creator_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 host_ids_json TEXT NOT NULL
);
CREATE TABLE user_totp (
 user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 secret_enc TEXT NOT NULL,
 confirmed INTEGER NOT NULL DEFAULT 0,
 last_step INTEGER NOT NULL DEFAULT -1
);
CREATE TABLE passkeys (
 id TEXT PRIMARY KEY,
 user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 name TEXT NOT NULL,
 credential_json TEXT NOT NULL,
 created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX passkeys_user ON passkeys(user_id);
CREATE TABLE auth_recovery_codes (
 code_hash TEXT PRIMARY KEY,
 user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE
);
CREATE TABLE auth_challenges (
 token_hash TEXT PRIMARY KEY,
 user_id TEXT REFERENCES users(id) ON DELETE CASCADE,
 purpose TEXT NOT NULL,
 payload TEXT NOT NULL DEFAULT '{}',
 expires_at TEXT NOT NULL,
 attempts INTEGER NOT NULL DEFAULT 0
);
ALTER TABLE sessions ADD COLUMN mfa_verified_at TEXT;
ALTER TABLE sessions ADD COLUMN recovery_only INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN mfa_reenroll_required INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN calendar_check_error TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN calendar_checked_at TEXT NOT NULL DEFAULT '';
-- +goose Down
ALTER TABLE users DROP COLUMN calendar_checked_at;
ALTER TABLE users DROP COLUMN calendar_check_error;
ALTER TABLE users DROP COLUMN mfa_reenroll_required;
ALTER TABLE sessions DROP COLUMN recovery_only;
ALTER TABLE sessions DROP COLUMN mfa_verified_at;
DROP TABLE auth_challenges;
DROP TABLE auth_recovery_codes;
DROP INDEX passkeys_user;
DROP TABLE passkeys;
DROP TABLE user_totp;
DROP TABLE dynamic_links;
