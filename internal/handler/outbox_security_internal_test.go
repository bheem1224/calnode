package handler

import (
	"context"
	"testing"
)

func TestPaidConfirmationOutboxRollsBackWithPayment(t *testing.T) {
	h := securitySeed(t)
	securityExec(t, h, `INSERT INTO bookings(id,event_type_id,host_id,start_at,end_at,payment_status)VALUES('paid','event-a','member-a','2027-01-01T10:00:00Z','2027-01-01T10:30:00Z','pending')`)
	securityExec(t, h, `CREATE TRIGGER reject_confirmation BEFORE INSERT ON jobs WHEN NEW.type='booking.confirm' BEGIN SELECT RAISE(ABORT,'simulate durable queue failure');END`)
	if err := h.confirmPaidBooking(context.Background(), "paid", "payment", 1000, "usd"); err == nil {
		t.Fatal("queue failure acknowledged as a successful payment transition")
	}
	var status string
	if err := h.db.QueryRow(`SELECT payment_status FROM bookings WHERE id='paid'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "pending" {
		t.Fatal("payment committed without confirmation job")
	}
	securityExec(t, h, `DROP TRIGGER reject_confirmation`)
	if err := h.confirmPaidBooking(context.Background(), "paid", "payment", 1000, "usd"); err != nil {
		t.Fatal(err)
	}
	if err := h.confirmPaidBooking(context.Background(), "paid", "payment", 1000, "usd"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE id='booking.confirm:paid'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("durable confirmations=%d want 1", count)
	}
}
