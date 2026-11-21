-- +goose Up
ALTER TABLE webhook_deliveries ADD COLUMN source_event_id TEXT;
ALTER TABLE webhook_deliveries ADD COLUMN last_error TEXT;
ALTER TABLE webhook_deliveries ADD COLUMN event_type_id TEXT;
ALTER TABLE webhook_deliveries ADD COLUMN resource_id TEXT;
CREATE UNIQUE INDEX webhook_delivery_source ON webhook_deliveries(webhook_id,source_event_id);

-- Snapshot the transition inside the same SQLite transaction as its source.
-- The worker expands it using the subscriber's live permissions and field choices.
CREATE VIEW webhook_booking_snapshot AS SELECT b.id,b.event_type_id,json_object('occurred_at',b.updated_at,'id',b.id,'event_type_slug',e.slug,'host_id',b.host_id,'start_at',b.start_at,'end_at',b.end_at,'status',b.status,'created_at',b.created_at,'location_value',COALESCE(b.location_value,''),'cancellation_reason',COALESCE(b.cancellation_reason,''),'payment_status',CASE WHEN b.payment_status='none' THEN '' ELSE COALESCE(b.payment_status,'') END,'amount_paid_cents',COALESCE(b.amount_paid_cents,0),'amount_paid_currency',COALESCE(b.amount_paid_currency,'')) AS payload FROM bookings b JOIN event_types e ON e.id=b.event_type_id;

-- +goose StatementBegin
CREATE TRIGGER webhook_booking_created AFTER INSERT ON jobs WHEN NEW.type='booking.confirm' BEGIN
 INSERT OR IGNORE INTO jobs(id,type,payload,run_at,max_attempts)
 SELECT 'webhook.enqueue:created:'||s.id,'webhook.enqueue',
 json_object('event','booking.created','source_event_id','booking.created:'||s.id,'booking',json(s.payload)),
 strftime('%Y-%m-%dT%H:%M:%fZ','now'),24
 FROM webhook_booking_snapshot s WHERE s.id=json_extract(NEW.payload,'$.booking_id');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER webhook_booking_cancelled AFTER UPDATE OF status ON bookings WHEN NEW.status='cancelled' AND OLD.status<>'cancelled' BEGIN
 INSERT INTO jobs(id,type,payload,run_at,max_attempts)
 SELECT 'webhook.enqueue:'||lower(hex(randomblob(16))),'webhook.enqueue',
 json_object('event','booking.cancelled','source_event_id',lower(hex(randomblob(16))),'booking',json(json_set(s.payload,'$.occurred_at',strftime('%Y-%m-%dT%H:%M:%fZ','now')))),
 strftime('%Y-%m-%dT%H:%M:%fZ','now'),24 FROM webhook_booking_snapshot s WHERE s.id=NEW.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER webhook_booking_rescheduled AFTER UPDATE OF start_at,end_at,host_id ON bookings WHEN NEW.status='confirmed' AND (NEW.start_at<>OLD.start_at OR NEW.end_at<>OLD.end_at OR NEW.host_id<>OLD.host_id) BEGIN
 INSERT INTO jobs(id,type,payload,run_at,max_attempts)
 SELECT 'webhook.enqueue:'||lower(hex(randomblob(16))),'webhook.enqueue',
 json_object('event','booking.rescheduled','source_event_id',lower(hex(randomblob(16))),'booking',json(json_set(s.payload,'$.occurred_at',strftime('%Y-%m-%dT%H:%M:%fZ','now'),'$.previous_start_at',OLD.start_at,'$.previous_end_at',OLD.end_at))),
 strftime('%Y-%m-%dT%H:%M:%fZ','now'),24 FROM webhook_booking_snapshot s WHERE s.id=NEW.id;
END;
-- +goose StatementEnd

-- Recover only unfinished confirmations, without replaying historical deliveries.
INSERT OR IGNORE INTO jobs(id,type,payload,run_at,max_attempts)
 SELECT 'webhook.enqueue:created:'||s.id,'webhook.enqueue',
 json_object('event','booking.created','source_event_id','booking.created:'||s.id,'booking',json(s.payload)),
 strftime('%Y-%m-%dT%H:%M:%fZ','now'),24
 FROM webhook_booking_snapshot s JOIN bookings b ON b.id=s.id
 JOIN jobs j ON j.id='booking.confirm:'||s.id
 WHERE j.status IN ('pending','running') AND b.status='confirmed' AND b.pending_approval=0 AND b.payment_status<>'pending'
 AND NOT EXISTS(SELECT 1 FROM webhook_deliveries d WHERE d.booking_id=s.id AND d.event='booking.created');

-- Existing queued deliveries keep their identity and gain the extended retry budget.
UPDATE jobs SET max_attempts=24 WHERE type='webhook.deliver' AND status IN ('pending','running') AND max_attempts=3;

-- +goose Down
DROP TRIGGER webhook_booking_rescheduled;
DROP TRIGGER webhook_booking_cancelled;
DROP TRIGGER webhook_booking_created;
DROP VIEW webhook_booking_snapshot;
DELETE FROM jobs WHERE type='webhook.enqueue';
DROP INDEX webhook_delivery_source;
ALTER TABLE webhook_deliveries DROP COLUMN source_event_id;
ALTER TABLE webhook_deliveries DROP COLUMN last_error;
ALTER TABLE webhook_deliveries DROP COLUMN event_type_id;
ALTER TABLE webhook_deliveries DROP COLUMN resource_id;
