-- +goose Up
-- Scheduled timezone changes on users
ALTER TABLE users ADD COLUMN scheduled_timezone TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN timezone_effective_at TEXT NOT NULL DEFAULT '';

-- Multiple named availability schedules per user
CREATE TABLE IF NOT EXISTS availability_schedules (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    timezone   TEXT NOT NULL DEFAULT '',
    is_default INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_availability_schedules_user
    ON availability_schedules (user_id);

ALTER TABLE availability_rules ADD COLUMN schedule_id TEXT REFERENCES availability_schedules(id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS idx_availability_rules_schedule
    ON availability_rules (schedule_id);

ALTER TABLE event_types ADD COLUMN schedule_id TEXT REFERENCES availability_schedules(id) ON DELETE SET NULL;

-- Lightweight Rich Out-of-Office (OOO) & multi-date holiday blockouts
CREATE TABLE IF NOT EXISTS ooo_entries (
    id               TEXT PRIMARY KEY,
    user_id          TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    team_id          TEXT REFERENCES teams(id) ON DELETE CASCADE,
    dates_json       TEXT NOT NULL DEFAULT '[]',
    reason           TEXT NOT NULL DEFAULT 'ooo',
    public_note      TEXT NOT NULL DEFAULT '',
    redirect_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_ooo_entries_user ON ooo_entries (user_id);
CREATE INDEX IF NOT EXISTS idx_ooo_entries_team ON ooo_entries (team_id);

-- +goose Down
DROP TABLE IF EXISTS ooo_entries;
DROP INDEX IF EXISTS idx_availability_rules_schedule;
DROP TABLE IF EXISTS availability_schedules;
