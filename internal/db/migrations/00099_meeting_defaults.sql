-- +goose Up
ALTER TABLE users ADD COLUMN default_conferencing_type TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE users DROP COLUMN default_conferencing_type;
