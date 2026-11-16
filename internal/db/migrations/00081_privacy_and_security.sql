-- +goose Up
ALTER TABLE event_types ADD COLUMN hide_notes_in_calendar INTEGER NOT NULL DEFAULT 0;
ALTER TABLE event_types ADD COLUMN hide_calendar_event_details INTEGER NOT NULL DEFAULT 0;
ALTER TABLE event_types ADD COLUMN requires_confirmation INTEGER NOT NULL DEFAULT 0;
ALTER TABLE event_types ADD COLUMN confirm_mode TEXT NOT NULL DEFAULT 'always' CHECK (confirm_mode IN ('always', 'under_notice', 'free_email'));
ALTER TABLE event_types ADD COLUMN confirm_notice_minutes INTEGER NOT NULL DEFAULT 30;
ALTER TABLE event_types ADD COLUMN unconfirmed_blocks_slots INTEGER NOT NULL DEFAULT 1;
ALTER TABLE event_types ADD COLUMN require_email_verification INTEGER NOT NULL DEFAULT 0;

ALTER TABLE bookings ADD COLUMN pending_approval INTEGER NOT NULL DEFAULT 0;
DROP INDEX IF EXISTS idx_bookings_no_double;
CREATE UNIQUE INDEX IF NOT EXISTS idx_bookings_no_double
    ON bookings (host_id, start_at) WHERE status != 'cancelled' AND pending_approval = 0;

CREATE TABLE IF NOT EXISTS email_verification_codes (
    id              TEXT PRIMARY KEY,
    event_type_slug TEXT NOT NULL,
    email           TEXT NOT NULL,
    code            TEXT NOT NULL,
    token           TEXT NOT NULL DEFAULT '',
    verified        INTEGER NOT NULL DEFAULT 0,
    expires_at      TEXT NOT NULL,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_email_verification_lookup ON email_verification_codes (event_type_slug, email, expires_at);

-- +goose Down
DROP INDEX IF EXISTS idx_email_verification_lookup;
DROP TABLE IF EXISTS email_verification_codes;
DROP INDEX IF EXISTS idx_bookings_no_double;
ALTER TABLE bookings DROP COLUMN pending_approval;
CREATE UNIQUE INDEX IF NOT EXISTS idx_bookings_no_double
    ON bookings (host_id, start_at) WHERE status != 'cancelled';
ALTER TABLE event_types DROP COLUMN require_email_verification;
ALTER TABLE event_types DROP COLUMN unconfirmed_blocks_slots;
ALTER TABLE event_types DROP COLUMN confirm_notice_minutes;
ALTER TABLE event_types DROP COLUMN confirm_mode;
ALTER TABLE event_types DROP COLUMN requires_confirmation;
ALTER TABLE event_types DROP COLUMN hide_calendar_event_details;
ALTER TABLE event_types DROP COLUMN hide_notes_in_calendar;
