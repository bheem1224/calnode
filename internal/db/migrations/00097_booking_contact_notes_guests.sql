-- +goose Up
ALTER TABLE bookings ADD COLUMN contact_phone TEXT NOT NULL DEFAULT '';
ALTER TABLE bookings ADD COLUMN notes TEXT NOT NULL DEFAULT '';
ALTER TABLE bookings ADD COLUMN guests_json TEXT NOT NULL DEFAULT '[]';

-- +goose Down
SELECT 1;
