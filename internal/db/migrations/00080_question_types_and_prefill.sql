-- +goose Up
CREATE TABLE booking_answers_tmp (
    id          TEXT PRIMARY KEY,
    booking_id  TEXT NOT NULL,
    question_id TEXT NOT NULL,
    value       TEXT NOT NULL
);
INSERT INTO booking_answers_tmp (id, booking_id, question_id, value)
    SELECT id, booking_id, question_id, value FROM booking_answers;
DROP TABLE booking_answers;

CREATE TABLE event_type_questions_new (
    id                TEXT PRIMARY KEY,
    event_type_id     TEXT NOT NULL REFERENCES event_types(id) ON DELETE CASCADE,
    label             TEXT NOT NULL,
    type              TEXT NOT NULL CHECK (type IN (
        'text', 'select', 'checkbox',
        'number', 'phone', 'address', 'url',
        'multiselect', 'checkbox_group', 'radio_group', 'multiple_emails'
    )),
    options           TEXT,
    required          INTEGER NOT NULL DEFAULT 0,
    position          INTEGER NOT NULL DEFAULT 0,
    identifier        TEXT NOT NULL DEFAULT '',
    hidden            INTEGER NOT NULL DEFAULT 0,
    lock_if_prefilled INTEGER NOT NULL DEFAULT 0
);
INSERT INTO event_type_questions_new (id, event_type_id, label, type, options, required, position)
    SELECT id, event_type_id, label, type, options, required, position FROM event_type_questions;
DROP TABLE event_type_questions;
ALTER TABLE event_type_questions_new RENAME TO event_type_questions;

CREATE TABLE booking_answers (
    id          TEXT PRIMARY KEY,
    booking_id  TEXT NOT NULL REFERENCES bookings(id) ON DELETE CASCADE,
    question_id TEXT NOT NULL REFERENCES event_type_questions(id) ON DELETE CASCADE,
    value       TEXT NOT NULL
);
INSERT INTO booking_answers (id, booking_id, question_id, value)
    SELECT id, booking_id, question_id, value FROM booking_answers_tmp;
DROP TABLE booking_answers_tmp;

-- +goose Down
CREATE TABLE booking_answers_tmp (
    id          TEXT PRIMARY KEY,
    booking_id  TEXT NOT NULL,
    question_id TEXT NOT NULL,
    value       TEXT NOT NULL
);
INSERT INTO booking_answers_tmp (id, booking_id, question_id, value)
    SELECT id, booking_id, question_id, value FROM booking_answers;
DROP TABLE booking_answers;

CREATE TABLE event_type_questions_old (
    id            TEXT PRIMARY KEY,
    event_type_id TEXT NOT NULL REFERENCES event_types(id) ON DELETE CASCADE,
    label         TEXT NOT NULL,
    type          TEXT NOT NULL CHECK (type IN ('text', 'select', 'checkbox')),
    options       TEXT,
    required      INTEGER NOT NULL DEFAULT 0,
    position      INTEGER NOT NULL DEFAULT 0
);
INSERT INTO event_type_questions_old (id, event_type_id, label, type, options, required, position)
    SELECT id, event_type_id, label,
        CASE WHEN type IN ('text', 'select', 'checkbox') THEN type ELSE 'text' END,
        options, required, position
    FROM event_type_questions;
DROP TABLE event_type_questions;
ALTER TABLE event_type_questions_old RENAME TO event_type_questions;

CREATE TABLE booking_answers (
    id          TEXT PRIMARY KEY,
    booking_id  TEXT NOT NULL REFERENCES bookings(id) ON DELETE CASCADE,
    question_id TEXT NOT NULL REFERENCES event_type_questions(id) ON DELETE CASCADE,
    value       TEXT NOT NULL
);
INSERT INTO booking_answers (id, booking_id, question_id, value)
    SELECT id, booking_id, question_id, value FROM booking_answers_tmp;
DROP TABLE booking_answers_tmp;
