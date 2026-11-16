-- +goose Up
ALTER TABLE event_types ADD COLUMN destination_calendar_id TEXT NOT NULL DEFAULT '';
ALTER TABLE server_settings ADD COLUMN microsoft_allow_personal_calendars INTEGER NOT NULL DEFAULT 0;
ALTER TABLE server_settings ADD COLUMN microsoft_cal_client_id TEXT NOT NULL DEFAULT '';
ALTER TABLE server_settings ADD COLUMN microsoft_cal_client_secret_enc TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE server_settings DROP COLUMN microsoft_cal_client_secret_enc;
ALTER TABLE server_settings DROP COLUMN microsoft_cal_client_id;
ALTER TABLE server_settings DROP COLUMN microsoft_allow_personal_calendars;
ALTER TABLE event_types DROP COLUMN destination_calendar_id;
