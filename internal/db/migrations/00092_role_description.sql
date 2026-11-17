-- +goose Up
ALTER TABLE roles ADD COLUMN description TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE roles DROP COLUMN description;
