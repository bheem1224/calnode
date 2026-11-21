-- +goose Up
ALTER TABLE webhooks ADD COLUMN scopes_json TEXT;
ALTER TABLE single_use_tokens ADD COLUMN booked_notified_at TEXT;
-- Historical bookings have already emitted their notification; do not replay them.
UPDATE single_use_tokens SET booked_notified_at=used_at WHERE used_at IS NOT NULL;
CREATE INDEX single_use_booked_pending ON single_use_tokens(used_at) WHERE booked_notified_at IS NULL AND used_at IS NOT NULL;
CREATE INDEX single_use_expiry_pending ON single_use_tokens(expires_at) WHERE expired_notified_at IS NULL AND used_at IS NULL;

-- +goose Down
-- An old release cannot interpret multi-scope subscriptions. Disable rather than broaden them.
UPDATE webhooks SET is_active=0 WHERE scopes_json IS NOT NULL;
DROP INDEX single_use_booked_pending;
DROP INDEX single_use_expiry_pending;
ALTER TABLE webhooks DROP COLUMN scopes_json;
ALTER TABLE single_use_tokens DROP COLUMN booked_notified_at;
