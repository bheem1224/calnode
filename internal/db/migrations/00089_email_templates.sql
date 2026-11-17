-- +goose Up
ALTER TABLE event_types ADD COLUMN html_confirmation TEXT;
ALTER TABLE event_types ADD COLUMN html_cancellation TEXT;
ALTER TABLE event_types ADD COLUMN html_reschedule TEXT;
ALTER TABLE event_types ADD COLUMN html_reminder TEXT;

CREATE TABLE IF NOT EXISTS email_templates (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('confirmation', 'cancellation', 'reschedule', 'reminder')),
    is_default INTEGER NOT NULL DEFAULT 0,
    mode TEXT NOT NULL DEFAULT 'note' CHECK (mode IN ('note', 'html')),
    subject TEXT NOT NULL DEFAULT '',
    body TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_email_templates_user_kind ON email_templates(user_id, kind);

-- +goose Down
SELECT 1;
