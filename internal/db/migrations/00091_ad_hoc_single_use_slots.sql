-- +goose Up
ALTER TABLE single_use_tokens ADD COLUMN custom_slots_json TEXT;

-- +goose Down
ALTER TABLE single_use_tokens DROP COLUMN custom_slots_json;
