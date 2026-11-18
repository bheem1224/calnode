-- +goose Up
ALTER TABLE server_settings ADD COLUMN signin_policy_json TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN enrollment_source TEXT NOT NULL DEFAULT 'existing';
ALTER TABLE users ADD COLUMN microsoft_subject TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN auth_method TEXT NOT NULL DEFAULT 'legacy';
CREATE UNIQUE INDEX users_microsoft_subject ON users(microsoft_subject) WHERE microsoft_subject <> '';
CREATE TABLE signin_intents (
  state_hash TEXT PRIMARY KEY,
  invite_hash TEXT NOT NULL,
  expires_at TEXT NOT NULL
);

-- +goose Down
DROP TABLE signin_intents;
DROP INDEX users_microsoft_subject;
ALTER TABLE sessions DROP COLUMN auth_method;
ALTER TABLE users DROP COLUMN microsoft_subject;
ALTER TABLE users DROP COLUMN enrollment_source;
ALTER TABLE server_settings DROP COLUMN signin_policy_json;
