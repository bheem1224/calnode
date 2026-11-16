-- +goose Up
ALTER TABLE event_types ADD COLUMN allow_multiple_durations INTEGER NOT NULL DEFAULT 0;
ALTER TABLE event_types ADD COLUMN duration_options TEXT;
ALTER TABLE event_types ADD COLUMN location_options TEXT;

-- +goose Down
ALTER TABLE event_types DROP COLUMN location_options;
ALTER TABLE event_types DROP COLUMN duration_options;
ALTER TABLE event_types DROP COLUMN allow_multiple_durations;
