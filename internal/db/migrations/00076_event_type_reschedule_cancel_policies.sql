-- +goose Up
ALTER TABLE event_types ADD COLUMN disable_cancel INTEGER NOT NULL DEFAULT 0;
ALTER TABLE event_types ADD COLUMN disable_reschedule INTEGER NOT NULL DEFAULT 0;
ALTER TABLE event_types ADD COLUMN require_cancel_reason TEXT NOT NULL DEFAULT 'optional' CHECK (require_cancel_reason IN ('none', 'optional', 'required'));
ALTER TABLE event_types ADD COLUMN allow_reschedule_past INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE event_types DROP COLUMN allow_reschedule_past;
ALTER TABLE event_types DROP COLUMN require_cancel_reason;
ALTER TABLE event_types DROP COLUMN disable_reschedule;
ALTER TABLE event_types DROP COLUMN disable_cancel;
