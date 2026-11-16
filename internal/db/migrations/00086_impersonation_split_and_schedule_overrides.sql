-- +goose Up
-- Separate actor capability (allow_impersonation / can_impersonate) from target policy (is_impersonatable).
-- Built-in admin can impersonate members (allow_impersonation=1, is_impersonatable=0).
-- Built-in member cannot impersonate anyone (allow_impersonation=0, is_impersonatable=1).
ALTER TABLE roles ADD COLUMN is_impersonatable INTEGER NOT NULL DEFAULT 1;
UPDATE roles SET allow_impersonation = 1, is_impersonatable = 0 WHERE id = 'role_admin';
UPDATE roles SET allow_impersonation = 0, is_impersonatable = 1 WHERE id = 'role_member';

-- Per-schedule date overrides (NULL = global/default schedule).
ALTER TABLE availability_overrides ADD COLUMN schedule_id TEXT REFERENCES availability_schedules(id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS idx_availability_overrides_schedule
    ON availability_overrides (schedule_id);

-- +goose Down
DROP INDEX IF EXISTS idx_availability_overrides_schedule;
