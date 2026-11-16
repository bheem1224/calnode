-- +goose Up
-- Granular RBAC roles (built-in 'admin' and 'member' do NOT grant allow_impersonation by default).
CREATE TABLE IF NOT EXISTS roles (
    id                  TEXT PRIMARY KEY,
    name                TEXT NOT NULL UNIQUE,
    is_system           INTEGER NOT NULL DEFAULT 0,
    allow_impersonation INTEGER NOT NULL DEFAULT 0,
    can_manage_users    INTEGER NOT NULL DEFAULT 0,
    can_manage_settings INTEGER NOT NULL DEFAULT 0,
    can_manage_teams    INTEGER NOT NULL DEFAULT 0,
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

INSERT OR IGNORE INTO roles (id, name, is_system, allow_impersonation, can_manage_users, can_manage_settings, can_manage_teams)
VALUES
    ('role_admin', 'admin', 1, 0, 1, 1, 1),
    ('role_member', 'member', 1, 0, 0, 0, 0);

ALTER TABLE users ADD COLUMN custom_role_id TEXT REFERENCES roles(id) ON DELETE SET NULL;

-- Session-level impersonation tracking (impersonator_user_id is non-empty while impersonating).
ALTER TABLE sessions ADD COLUMN impersonator_user_id TEXT REFERENCES users(id) ON DELETE SET NULL;

CREATE TABLE IF NOT EXISTS impersonation_audit_logs (
    id             TEXT PRIMARY KEY,
    actor_user_id  TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    target_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    action         TEXT NOT NULL CHECK (action IN ('start', 'stop')),
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_impersonation_audit_created
    ON impersonation_audit_logs (created_at DESC);

-- +goose Down
DROP INDEX IF EXISTS idx_impersonation_audit_created;
DROP TABLE IF EXISTS impersonation_audit_logs;
DROP TABLE IF EXISTS roles;
