-- +goose Up
ALTER TABLE event_types ADD COLUMN optimized_slots INTEGER NOT NULL DEFAULT 0;
ALTER TABLE event_types ADD COLUMN booking_form_config TEXT NOT NULL DEFAULT '';

-- +goose Down
SELECT 1;
