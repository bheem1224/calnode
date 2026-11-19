-- +goose Up
CREATE TABLE abuse_events (
    id INTEGER PRIMARY KEY,
    bucket TEXT NOT NULL,
    cost INTEGER NOT NULL,
    occurred_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);
CREATE INDEX abuse_events_bucket_time ON abuse_events(bucket, occurred_at);
CREATE INDEX abuse_events_expiry ON abuse_events(expires_at);
CREATE TABLE login_backoff (
    account_hash TEXT PRIMARY KEY,
    failures INTEGER NOT NULL,
    retry_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);
CREATE INDEX login_backoff_expiry ON login_backoff(expires_at);
CREATE INDEX oauth_tokens_client ON oauth_access_tokens(client_id);
CREATE INDEX oauth_codes_client ON oauth_auth_codes(client_id);
ALTER TABLE email_verification_codes ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE email_verification_codes DROP COLUMN attempts;
DROP INDEX oauth_codes_client;
DROP INDEX oauth_tokens_client;
DROP TABLE login_backoff;
DROP TABLE abuse_events;
