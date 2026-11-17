-- +goose Up
ALTER TABLE teams ADD COLUMN show_members_publicly INTEGER NOT NULL DEFAULT 0;

-- +goose Down
SELECT 1;
