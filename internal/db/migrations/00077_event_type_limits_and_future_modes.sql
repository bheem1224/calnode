-- +goose Up
ALTER TABLE event_types ADD COLUMN max_per_month INTEGER;
ALTER TABLE event_types ADD COLUMN max_per_year INTEGER;
ALTER TABLE event_types ADD COLUMN max_duration_per_day INTEGER;
ALTER TABLE event_types ADD COLUMN max_duration_per_week INTEGER;
ALTER TABLE event_types ADD COLUMN show_first_slot_only INTEGER NOT NULL DEFAULT 0;
ALTER TABLE event_types ADD COLUMN future_limit_mode TEXT NOT NULL DEFAULT 'calendar_days' CHECK (future_limit_mode IN ('calendar_days', 'business_days', 'date_range'));
ALTER TABLE event_types ADD COLUMN future_range_start TEXT;
ALTER TABLE event_types ADD COLUMN future_range_end TEXT;

-- +goose Down
ALTER TABLE event_types DROP COLUMN future_range_end;
ALTER TABLE event_types DROP COLUMN future_range_start;
ALTER TABLE event_types DROP COLUMN future_limit_mode;
ALTER TABLE event_types DROP COLUMN show_first_slot_only;
ALTER TABLE event_types DROP COLUMN max_duration_per_week;
ALTER TABLE event_types DROP COLUMN max_duration_per_day;
ALTER TABLE event_types DROP COLUMN max_per_year;
ALTER TABLE event_types DROP COLUMN max_per_month;
