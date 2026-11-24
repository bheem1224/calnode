-- +goose Up
ALTER TABLE single_use_tokens ADD COLUMN prefill_json TEXT;

-- +goose Down
ALTER TABLE single_use_tokens DROP COLUMN prefill_json;
