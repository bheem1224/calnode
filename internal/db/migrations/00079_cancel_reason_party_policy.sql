-- +goose Up
ALTER TABLE event_types ADD COLUMN require_cancel_reason_v2 TEXT NOT NULL DEFAULT 'optional' CHECK (require_cancel_reason_v2 IN ('none', 'optional', 'required', 'both', 'host_only', 'attendee_only'));
UPDATE event_types SET require_cancel_reason_v2 = require_cancel_reason;
ALTER TABLE event_types DROP COLUMN require_cancel_reason;
ALTER TABLE event_types RENAME COLUMN require_cancel_reason_v2 TO require_cancel_reason;

-- +goose Down
ALTER TABLE event_types ADD COLUMN require_cancel_reason_v1 TEXT NOT NULL DEFAULT 'optional' CHECK (require_cancel_reason_v1 IN ('none', 'optional', 'required'));
UPDATE event_types SET require_cancel_reason_v1 = CASE
  WHEN require_cancel_reason IN ('both', 'attendee_only') THEN 'required'
  WHEN require_cancel_reason = 'host_only' THEN 'optional'
  ELSE require_cancel_reason
END;
ALTER TABLE event_types DROP COLUMN require_cancel_reason;
ALTER TABLE event_types RENAME COLUMN require_cancel_reason_v1 TO require_cancel_reason;
