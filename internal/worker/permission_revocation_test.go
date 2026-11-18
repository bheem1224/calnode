package worker_test

import (
	"context"
	"github.com/calnode/calnode/internal/webhook"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestQueuedWebhookRevocationPreventsDelivery(t *testing.T) {
	database, svc := setup(t)
	ctx := context.Background()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(200) }))
	defer server.Close()
	_, _, err := svc.Create(ctx, "host-01", server.URL, []string{"booking.created"})
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.Enqueue(ctx, "booking.created", webhook.BookingPayload{HostID: "host-01", Status: "confirmed"}); err != nil {
		t.Fatal(err)
	}
	var queued int
	if err = database.QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&queued); err != nil || queued != 1 {
		t.Fatalf("queued=%d err=%v", queued, err)
	}
	if _, err = database.Exec(`INSERT INTO roles(id,name,permissions_json) VALUES('restricted','Restricted','{}')`); err != nil {
		t.Fatal(err)
	}
	if _, err = database.Exec(`UPDATE users SET custom_role_id='restricted',legacy_permissions=0 WHERE id='host-01'`); err != nil {
		t.Fatal(err)
	}
	newWorker(t, database, svc).Poll(ctx)
	if calls.Load() != 0 {
		t.Fatal("queued payload delivered after permission revocation")
	}
}
