-- +goose Up
ALTER TABLE calendar_event_intents ADD COLUMN booking_id TEXT NOT NULL DEFAULT '';
ALTER TABLE calendar_event_intents ADD COLUMN assignment_id TEXT NOT NULL DEFAULT '';
ALTER TABLE calendar_event_intents ADD COLUMN event_id TEXT NOT NULL DEFAULT '';
ALTER TABLE calendar_event_intents ADD COLUMN write_started INTEGER NOT NULL DEFAULT 1;
ALTER TABLE calendar_event_intents ADD COLUMN retired INTEGER NOT NULL DEFAULT 0;
-- Repair legacy primary-seat omissions before operation identities are bound.
INSERT OR IGNORE INTO booking_hosts(id,booking_id,user_id,is_primary,external_event_id)
 SELECT lower(hex(randomblob(16))),b.id,b.host_id,1,b.external_event_id FROM bookings b
 WHERE NOT EXISTS(SELECT 1 FROM booking_hosts bh WHERE bh.booking_id=b.id AND bh.is_primary=1);
UPDATE booking_hosts SET is_primary=1 WHERE user_id=(SELECT host_id FROM bookings WHERE id=booking_hosts.booking_id)
 AND NOT EXISTS(SELECT 1 FROM booking_hosts bh WHERE bh.booking_id=booking_hosts.booking_id AND bh.is_primary=1);
UPDATE calendar_event_intents SET
 booking_id=COALESCE((SELECT booking_id FROM booking_hosts WHERE 'booking-host:'||id=operation_key AND user_id=calendar_event_intents.user_id),''),
 assignment_id=COALESCE((SELECT id FROM booking_hosts WHERE 'booking-host:'||id=operation_key AND user_id=calendar_event_intents.user_id),'');
CREATE INDEX calendar_intents_booking ON calendar_event_intents(booking_id);
CREATE TABLE calendar_event_cleanup (
 id TEXT PRIMARY KEY,
 booking_id TEXT NOT NULL,
 user_id TEXT NOT NULL,
 provider TEXT NOT NULL DEFAULT '',
 calendar_ref TEXT NOT NULL DEFAULT '',
 event_id TEXT NOT NULL DEFAULT '',
 operation_key TEXT NOT NULL DEFAULT '',
 attempts INTEGER NOT NULL DEFAULT 0,
 next_attempt_at TEXT NOT NULL DEFAULT '',
 done INTEGER NOT NULL DEFAULT 0,
 created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
 last_error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX calendar_cleanup_pending ON calendar_event_cleanup(done,next_attempt_at);
CREATE INDEX calendar_cleanup_booking ON calendar_event_cleanup(booking_id);

-- The view keeps lifecycle triggers on the same target-resolution rules. It
-- includes legacy bookings whose primary event ID predates booking_hosts.
CREATE VIEW calendar_cleanup_targets AS
 SELECT CASE WHEN i.operation_key IS NOT NULL THEN 'intent:'||bh.user_id||':'||i.operation_key ELSE 'seat:'||bh.id END AS id,b.id AS booking_id,bh.user_id,
 COALESCE(i.provider,bh.external_provider,'') AS provider,
 COALESCE(i.calendar_ref,bh.external_calendar_id,'') AS calendar_ref,
 COALESCE(NULLIF(i.event_id,''),NULLIF(bh.external_event_id,''),CASE WHEN bh.is_primary=1 THEN b.external_event_id END,'') AS event_id,
 COALESCE(i.operation_key,'') AS operation_key,bh.id AS assignment_id,bh.is_primary
 FROM booking_hosts bh JOIN bookings b ON b.id=bh.booking_id
 LEFT JOIN calendar_event_intents i ON i.user_id=bh.user_id AND i.operation_key='booking-host:'||bh.id
 UNION ALL
 SELECT 'legacy:'||b.id||':'||b.host_id,b.id,b.host_id,'','',b.external_event_id,'','',1
 FROM bookings b WHERE COALESCE(b.external_event_id,'')<>'' AND NOT EXISTS(SELECT 1 FROM booking_hosts bh WHERE bh.booking_id=b.id AND bh.is_primary=1);

-- +goose StatementBegin
CREATE TRIGGER booking_cancel_cleanup BEFORE UPDATE OF status ON bookings WHEN NEW.status='cancelled' AND OLD.status<>'cancelled'
BEGIN
 INSERT OR IGNORE INTO calendar_event_cleanup(id,booking_id,user_id,provider,calendar_ref,event_id,operation_key)
 SELECT id,booking_id,user_id,provider,calendar_ref,event_id,operation_key FROM calendar_cleanup_targets
 WHERE (booking_id=OLD.id) AND (event_id<>'' OR operation_key<>'');
 UPDATE calendar_event_intents SET retired=1 WHERE booking_id=OLD.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER booking_delete_cleanup BEFORE DELETE ON bookings WHEN 1
BEGIN
 INSERT OR IGNORE INTO calendar_event_cleanup(id,booking_id,user_id,provider,calendar_ref,event_id,operation_key)
 SELECT id,booking_id,user_id,provider,calendar_ref,event_id,operation_key FROM calendar_cleanup_targets
 WHERE (booking_id=OLD.id) AND (event_id<>'' OR operation_key<>'');
 UPDATE calendar_event_intents SET retired=1 WHERE booking_id=OLD.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER booking_reassign_cleanup BEFORE UPDATE OF host_id ON bookings WHEN OLD.host_id<>NEW.host_id
BEGIN
 INSERT OR IGNORE INTO calendar_event_cleanup(id,booking_id,user_id,provider,calendar_ref,event_id,operation_key)
 SELECT id,booking_id,user_id,provider,calendar_ref,event_id,operation_key FROM calendar_cleanup_targets
 WHERE (booking_id=OLD.id AND is_primary=1) AND (event_id<>'' OR operation_key<>'');
 UPDATE calendar_event_intents SET retired=1 WHERE user_id=OLD.host_id AND assignment_id IN(SELECT id FROM booking_hosts WHERE booking_id=OLD.id AND is_primary=1);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER seat_move_cleanup BEFORE UPDATE OF id,user_id ON booking_hosts WHEN OLD.id<>NEW.id OR OLD.user_id<>NEW.user_id
BEGIN
 INSERT OR IGNORE INTO calendar_event_cleanup(id,booking_id,user_id,provider,calendar_ref,event_id,operation_key)
 SELECT id,booking_id,user_id,provider,calendar_ref,event_id,operation_key FROM calendar_cleanup_targets
 WHERE (assignment_id=OLD.id) AND (event_id<>'' OR operation_key<>'');
 UPDATE calendar_event_intents SET retired=1 WHERE user_id=OLD.user_id AND operation_key='booking-host:'||OLD.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER seat_delete_cleanup BEFORE DELETE ON booking_hosts WHEN 1
BEGIN
 INSERT OR IGNORE INTO calendar_event_cleanup(id,booking_id,user_id,provider,calendar_ref,event_id,operation_key)
 SELECT id,booking_id,user_id,provider,calendar_ref,event_id,operation_key FROM calendar_cleanup_targets
 WHERE (assignment_id=OLD.id) AND (event_id<>'' OR operation_key<>'');
 UPDATE calendar_event_intents SET retired=1 WHERE user_id=OLD.user_id AND operation_key='booking-host:'||OLD.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER calendar_intent_insert AFTER INSERT ON calendar_event_intents
WHEN NEW.booking_id<>'' AND NOT EXISTS(SELECT 1 FROM bookings b JOIN booking_hosts bh ON bh.booking_id=b.id WHERE b.id=NEW.booking_id AND b.status='confirmed' AND bh.id=NEW.assignment_id AND bh.user_id=NEW.user_id)
BEGIN
 UPDATE calendar_event_intents SET retired=1 WHERE user_id=NEW.user_id AND operation_key=NEW.operation_key;
END;
-- +goose StatementEnd

-- An acknowledgement may arrive after cancellation/reassignment. Refresh the
-- durable task with its recovered ID even if a previous lookup found no event.
-- +goose StatementBegin
CREATE TRIGGER calendar_intent_retired AFTER UPDATE OF retired,event_id ON calendar_event_intents
WHEN NEW.retired=1 AND NEW.booking_id<>''
BEGIN
 INSERT INTO calendar_event_cleanup(id,booking_id,user_id,provider,calendar_ref,event_id,operation_key)
 VALUES('intent:'||NEW.user_id||':'||NEW.operation_key,NEW.booking_id,NEW.user_id,NEW.provider,NEW.calendar_ref,NEW.event_id,NEW.operation_key)
 ON CONFLICT(id) DO UPDATE SET event_id=CASE WHEN excluded.event_id<>'' THEN excluded.event_id ELSE calendar_event_cleanup.event_id END,
 done=CASE WHEN excluded.event_id<>'' AND excluded.event_id<>calendar_event_cleanup.event_id THEN 0 ELSE calendar_event_cleanup.done END,
 next_attempt_at=CASE WHEN excluded.event_id<>'' AND excluded.event_id<>calendar_event_cleanup.event_id THEN '' ELSE calendar_event_cleanup.next_attempt_at END;
END;
-- +goose StatementEnd

INSERT OR IGNORE INTO calendar_event_cleanup(id,booking_id,user_id,provider,calendar_ref,event_id,operation_key)
 SELECT id,booking_id,user_id,provider,calendar_ref,event_id,operation_key FROM calendar_cleanup_targets
 WHERE booking_id IN(SELECT id FROM bookings WHERE status='cancelled') AND (event_id<>'' OR operation_key<>'');
UPDATE calendar_event_intents SET retired=1 WHERE booking_id IN(SELECT id FROM bookings WHERE status='cancelled');

CREATE TABLE zoom_meeting_intents (
 operation_key TEXT PRIMARY KEY,
 booking_id TEXT NOT NULL DEFAULT '',
 user_id TEXT NOT NULL,
 marker TEXT NOT NULL,
 topic TEXT NOT NULL,
 state TEXT NOT NULL DEFAULT 'new',
 meeting_id TEXT NOT NULL DEFAULT '',
 join_url TEXT NOT NULL DEFAULT '',
 cleanup_requested INTEGER NOT NULL DEFAULT 0,
 cleanup_done INTEGER NOT NULL DEFAULT 0,
 attempts INTEGER NOT NULL DEFAULT 0,
 next_attempt_at TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
 last_error TEXT NOT NULL DEFAULT ''
);
INSERT OR IGNORE INTO zoom_meeting_intents(operation_key,booking_id,user_id,marker,topic,state,meeting_id,join_url,cleanup_requested)
 SELECT 'booking:'||id,id,host_id,'','','ready',zoom_meeting_id,COALESCE(location_value,''),CASE WHEN status='cancelled' THEN 1 ELSE 0 END FROM bookings WHERE COALESCE(zoom_meeting_id,'')<>'';
CREATE INDEX zoom_intents_booking ON zoom_meeting_intents(booking_id);
-- Persist time drift even when creation is still awaiting its acknowledgement.
-- +goose StatementBegin
CREATE TRIGGER booking_time_sync AFTER UPDATE OF start_at,end_at ON bookings
WHEN NEW.status='confirmed' AND (NEW.start_at<>OLD.start_at OR NEW.end_at<>OLD.end_at)
BEGIN
 UPDATE booking_hosts SET needs_sync=1 WHERE booking_id=NEW.id;
 UPDATE zoom_meeting_intents SET next_attempt_at='' WHERE booking_id=NEW.id;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER booking_cancel_zoom AFTER UPDATE OF status ON bookings WHEN NEW.status='cancelled'
BEGIN
 UPDATE zoom_meeting_intents SET cleanup_requested=1,next_attempt_at='' WHERE booking_id=OLD.id;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER booking_delete_zoom BEFORE DELETE ON bookings
BEGIN
 UPDATE zoom_meeting_intents SET cleanup_requested=1,next_attempt_at='' WHERE booking_id=OLD.id;
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER booking_time_sync;
DROP TRIGGER booking_delete_zoom;
DROP TRIGGER booking_cancel_zoom;
DROP TABLE zoom_meeting_intents;
DROP TRIGGER calendar_intent_retired;
DROP TRIGGER calendar_intent_insert;
DROP TRIGGER seat_delete_cleanup;
DROP TRIGGER seat_move_cleanup;
DROP TRIGGER booking_reassign_cleanup;
DROP TRIGGER booking_delete_cleanup;
DROP TRIGGER booking_cancel_cleanup;
DROP VIEW calendar_cleanup_targets;
DROP TABLE calendar_event_cleanup;
DROP INDEX calendar_intents_booking;
ALTER TABLE calendar_event_intents DROP COLUMN retired;
ALTER TABLE calendar_event_intents DROP COLUMN write_started;
ALTER TABLE calendar_event_intents DROP COLUMN event_id;
ALTER TABLE calendar_event_intents DROP COLUMN assignment_id;
ALTER TABLE calendar_event_intents DROP COLUMN booking_id;
