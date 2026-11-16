-- +goose Up
ALTER TABLE webhooks ADD COLUMN event_type_id TEXT REFERENCES event_types(id) ON DELETE CASCADE;
ALTER TABLE single_use_tokens ADD COLUMN expired_notified_at TEXT;

-- +goose Down
ALTER TABLE single_use_tokens DROP COLUMN expired_notified_at;
ALTER TABLE webhooks DROP COLUMN event_type_id;
