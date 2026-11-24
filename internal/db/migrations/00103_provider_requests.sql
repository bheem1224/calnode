-- +goose Up
CREATE TABLE provider_backoff(scope TEXT PRIMARY KEY,until_at TEXT NOT NULL);
CREATE TABLE calendar_event_intents(
 user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 operation_key TEXT NOT NULL,
 provider TEXT NOT NULL,
 calendar_ref TEXT NOT NULL,
 created_at TEXT NOT NULL DEFAULT (datetime('now')),
 PRIMARY KEY(user_id,operation_key)
);

-- +goose Down
DROP TABLE calendar_event_intents;
DROP TABLE provider_backoff;
