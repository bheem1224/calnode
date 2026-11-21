package worker_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/calnode/calnode/internal/webhook"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestStaleWebhookWorkerCannotFailNewLease(t *testing.T) {
	d, s := setup(t)
	ctx := context.Background()
	_, _, err := s.Create(ctx, "host-01", "https://example.test/hook", []string{"booking.created"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Enqueue(ctx, "booking.created", webhook.BookingPayload{HostID: "host-01"}); err != nil {
		t.Fatal(err)
	}
	if _, err = d.Exec(`UPDATE jobs SET max_attempts=1 WHERE type='webhook.deliver'`); err != nil {
		t.Fatal(err)
	}
	newLease := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	w := newWorker(t, d, s)
	w.RegisterHandler("webhook.deliver", func(ctx context.Context, _ string) error {
		_, err := d.ExecContext(ctx, `UPDATE jobs SET locked_until=? WHERE type='webhook.deliver'`, newLease)
		if err != nil {
			t.Fatal(err)
		}
		return errors.New("old worker completed after its lease was replaced")
	})
	w.Poll(ctx)
	var jobStatus, lease, deliveryStatus string
	if err = d.QueryRow(`SELECT j.status,j.locked_until,d.status FROM jobs j JOIN webhook_deliveries d ON d.id=json_extract(j.payload,'$.webhook_delivery_id') WHERE j.type='webhook.deliver'`).Scan(&jobStatus, &lease, &deliveryStatus); err != nil {
		t.Fatal(err)
	}
	if jobStatus != "running" || lease != newLease || deliveryStatus != "pending" {
		t.Fatalf("stale worker changed new lease: %s %s %s", jobStatus, lease, deliveryStatus)
	}
}

func TestBookingWebhookOutboxRollbackRecoveryAndStableDelivery(t *testing.T) {
	d, s := setup(t)
	ctx := context.Background()
	for _, q := range []string{`INSERT INTO event_types(id,user_id,slug,name,duration_minutes)VALUES('e','host-01','e','Event',30)`, `INSERT INTO bookings(id,event_type_id,host_id,start_at,end_at)VALUES('b','e','host-01','2026-12-01T10:00:00Z','2026-12-01T10:30:00Z')`} {
		if _, err := d.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	requests := 0
	body := ""
	deliveryID := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		deliveryID = r.Header.Get("X-Calnode-Delivery")
		var env map[string]any
		json.NewDecoder(r.Body).Decode(&env)
		raw, _ := json.Marshal(env)
		body = string(raw)
		w.WriteHeader(204)
	}))
	defer server.Close()
	hook, _, err := s.Create(ctx, "host-01", server.URL, []string{"booking.rescheduled"})
	if err != nil {
		t.Fatal(err)
	}
	// A queue-write failure must roll the scheduling change back too.
	_, err = d.Exec(`CREATE TRIGGER fail_outbox BEFORE INSERT ON jobs WHEN NEW.type='webhook.enqueue' BEGIN SELECT RAISE(ABORT,'outbox unavailable'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.Exec(`UPDATE bookings SET start_at='2026-12-02T10:00:00Z',end_at='2026-12-02T10:30:00Z' WHERE id='b'`); err == nil {
		t.Fatal("booking committed without a durable event")
	}
	var start string
	d.QueryRow(`SELECT start_at FROM bookings WHERE id='b'`).Scan(&start)
	if start != "2026-12-01T10:00:00Z" {
		t.Fatal("outbox failure did not roll back booking")
	}
	d.Exec(`DROP TRIGGER fail_outbox`)
	_, err = d.Exec(`UPDATE bookings SET start_at='2026-12-02T10:00:00Z',end_at='2026-12-02T10:30:00Z' WHERE id='b'`)
	if err != nil {
		t.Fatal(err)
	}
	// Expansion can fail independently; the committed source survives and retries.
	d.Exec(`CREATE TRIGGER fail_delivery BEFORE INSERT ON jobs WHEN NEW.type='webhook.deliver' BEGIN SELECT RAISE(ABORT,'delivery queue unavailable'); END`)
	w := newWorker(t, d, s)
	w.Poll(ctx)
	var pending, n int
	d.QueryRow(`SELECT COUNT(*) FROM jobs WHERE type='webhook.enqueue' AND status='pending'`).Scan(&pending)
	d.QueryRow(`SELECT COUNT(*) FROM webhook_deliveries`).Scan(&n)
	if pending != 1 || n != 0 {
		t.Fatalf("failed expansion lost source or committed partial delivery: %d %d", pending, n)
	}
	d.Exec(`DROP TRIGGER fail_delivery`)
	d.Exec(`UPDATE jobs SET run_at='2000-01-01T00:00:00Z' WHERE type='webhook.enqueue'`)
	newWorker(t, d, s).Poll(ctx)
	newWorker(t, d, s).Poll(ctx)
	if requests != 1 || deliveryID == "" || body == "" {
		t.Fatal("persisted event not delivered", requests, body)
	}
	// Crash after expansion but before source completion must not create another ID.
	d.Exec(`UPDATE jobs SET status='pending',run_at='2000-01-01T00:00:00Z' WHERE type='webhook.enqueue'`)
	newWorker(t, d, s).Poll(ctx)
	newWorker(t, d, s).Poll(ctx)
	deliveries, err := s.ListDeliveries(ctx, "host-01", hook.ID)
	if err != nil || len(deliveries) != 1 || deliveries[0].ID != deliveryID || requests != 1 {
		t.Fatalf("source replay duplicated notification: %v %+v calls=%d", err, deliveries, requests)
	}
}
func TestEmptySingleUsePayloadStillDeliversAndCrashStatusIsConsistent(t *testing.T) {
	d, s := setup(t)
	ctx := context.Background()
	d.Exec(`INSERT INTO event_types(id,user_id,slug,name,duration_minutes)VALUES('e','host-01','e','Event',30)`)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var env struct{ Data map[string]any }
		json.NewDecoder(r.Body).Decode(&env)
		if len(env.Data) != 0 {
			t.Error("empty field selection leaked payload")
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	hook, _, err := s.CreateConfigured(ctx, "host-01", server.URL, []string{"single_use_link.expired"}, webhook.Options{Fields: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	err = s.EnqueueSingleUse(ctx, "single_use_link.expired", webhook.SingleUsePayload{ID: "token", EventTypeID: "e", HostID: "host-01", Token: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	newWorker(t, d, s).Poll(ctx)
	if calls != 1 {
		t.Fatal("field filtering removed authorization context")
	}
	err = s.EnqueueSingleUse(ctx, "single_use_link.expired", webhook.SingleUsePayload{ID: "second", EventTypeID: "e", HostID: "host-01"})
	if err != nil {
		t.Fatal(err)
	}
	d.Exec(`UPDATE jobs SET status='running',attempts=max_attempts,locked_until='2000-01-01T00:00:00Z' WHERE status='pending'`)
	newWorker(t, d, s).Poll(ctx)
	var pending int
	d.QueryRow(`SELECT COUNT(*) FROM webhook_deliveries WHERE webhook_id=? AND status='pending'`, hook.ID).Scan(&pending)
	if pending != 0 {
		t.Fatal("exhausted crash left a delivery pending")
	}
	// Owner can deliberately replay with the original id, but unrelated users cannot.
	var id string
	d.QueryRow(`SELECT id FROM webhook_deliveries WHERE status='failed'`).Scan(&id)
	if err = s.RetryDelivery(ctx, "host-02", hook.ID, id); err == nil {
		t.Fatal("foreign owner retried delivery")
	}
	if err = s.RetryDelivery(ctx, "host-01", hook.ID, id); err != nil {
		t.Fatal(err)
	}
	newWorker(t, d, s).Poll(ctx)
	if calls != 2 {
		t.Fatal("manual retry did not deliver")
	}
	var status string
	d.QueryRow(`SELECT status FROM webhook_deliveries WHERE id=?`, id).Scan(&status)
	if status != "success" {
		t.Fatal(status)
	}
}

func TestCreatedBookingSourceSurvivesCompletedConfirmationAndQueueFailure(t *testing.T) {
	d, s := setup(t)
	ctx := context.Background()
	for _, q := range []string{`INSERT INTO event_types(id,user_id,slug,name,duration_minutes)VALUES('created-event','host-01','created-event','Created',30)`, `INSERT INTO bookings(id,event_type_id,host_id,start_at,end_at)VALUES('created-booking','created-event','host-01','2026-12-01T10:00:00Z','2026-12-01T10:30:00Z')`, `INSERT INTO single_use_tokens(id,token,event_type_id,created_by_user_id,booking_id,used_at,created_at)VALUES('issued','opaque-token','created-event','host-01','created-booking','2026-11-21T10:00:00Z','2026-11-21T09:00:00Z')`} {
		if _, err := d.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var p struct {
			EventID string `json:"event_id"`
			Data    map[string]any
		}
		json.NewDecoder(r.Body).Decode(&p)
		if p.EventID != "booking.created:created-booking" || p.Data["event_type_id"] != "created-event" || p.Data["single_use_link_id"] != "issued" {
			t.Error("missing stable CRM correlation", p)
		}
		if _, ok := p.Data["payment_status"]; ok {
			t.Error("free booking payload changed")
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	_, _, err := s.CreateConfigured(ctx, "host-01", server.URL, []string{"booking.created"}, webhook.Options{Fields: []string{webhook.FieldID, webhook.FieldEventTypeID, webhook.FieldSingleUseLinkID, webhook.FieldPaymentStatus}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.Exec(`INSERT INTO jobs(id,type,payload,run_at)VALUES('booking.confirm:created-booking','booking.confirm','{"booking_id":"created-booking"}','2000-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	d.Exec(`CREATE TRIGGER created_queue_failure BEFORE INSERT ON jobs WHEN NEW.type='webhook.deliver' BEGIN SELECT RAISE(ABORT,'queue unavailable'); END`)
	w := newWorker(t, d, s)
	w.RegisterHandler("booking.confirm", func(context.Context, string) error { return nil })
	w.Poll(ctx)
	var done, pending int
	d.QueryRow(`SELECT COUNT(*) FROM jobs WHERE type='booking.confirm' AND status='done'`).Scan(&done)
	d.QueryRow(`SELECT COUNT(*) FROM jobs WHERE type='webhook.enqueue' AND status='pending'`).Scan(&pending)
	if done != 1 || pending != 1 {
		t.Fatalf("confirmation hid webhook failure: done=%d pending=%d", done, pending)
	}
	d.Exec(`DROP TRIGGER created_queue_failure`)
	d.Exec(`UPDATE jobs SET run_at='2000-01-01T00:00:00Z' WHERE type='webhook.enqueue'`)
	newWorker(t, d, s).Poll(ctx)
	newWorker(t, d, s).Poll(ctx)
	if calls != 1 {
		t.Fatal("committed booking event was lost", calls)
	}
}
