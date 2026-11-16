-- +goose Up
ALTER TABLE event_types ADD COLUMN calendar_event_title TEXT;
ALTER TABLE event_types ADD COLUMN redirect_url TEXT;
ALTER TABLE event_types ADD COLUMN reply_to TEXT;

-- +goose Down
ALTER TABLE event_types DROP COLUMN reply_to;
ALTER TABLE event_types DROP COLUMN redirect_url;
ALTER TABLE event_types DROP COLUMN calendar_event_title;
