package worker_test

import (
	"context"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/webhook"
)

func TestSingleUseLifecycle_AtomicRecoverySignedRetriesAndNoDuplicates(t *testing.T) {
	d, svc := setup(t)
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO event_types(id,user_id,slug,name,duration_minutes)VALUES('lifecycle','host-01','lifecycle','Lifecycle',30)`,
		`INSERT INTO bookings(id,event_type_id,host_id,start_at,end_at)VALUES('used-booking','lifecycle','host-01','2026-11-25T10:00:00Z','2026-11-25T10:30:00Z')`,
		`INSERT INTO single_use_tokens(id,token,event_type_id,created_by_user_id,expires_at,used_at,booking_id,created_at)VALUES('used-token','used-token-value','lifecycle','host-01','2020-01-01T00:00:00Z','2020-01-01T00:00:00Z','used-booking','2020-01-01T00:00:00Z'),('expired-token','expired-token-value','lifecycle','host-01','2020-01-01T00:00:00Z',NULL,NULL,'2020-01-01T00:00:00Z')`,
	} {
		if _, err := d.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	counts := map[string]int{}
	bodies := map[string]string{}
	var signingKey []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		id := r.Header.Get("X-Calnode-Delivery")
		if id == "" || r.Header.Get("X-Calnode-Signature") != webhook.Sign(signingKey, raw) {
			t.Error("missing identity or bad signature")
		}
		if prior, ok := bodies[id]; ok && prior != string(raw) {
			t.Error("retry changed the signed payload")
		}
		bodies[id] = string(raw)
		counts[id]++
		if counts[id] == 1 {
			w.WriteHeader(503)
		} else {
			w.WriteHeader(204)
		}
	}))
	defer server.Close()
	hook, secret, err := svc.Create(ctx, "host-01", server.URL, []string{"single_use_link.booked", "single_use_link.expired"})
	if err != nil {
		t.Fatal(err)
	}
	signingKey, _ = hex.DecodeString(secret)
	if _, err = d.Exec(`CREATE TRIGGER unavailable_queue BEFORE INSERT ON jobs WHEN NEW.type='webhook.deliver' BEGIN SELECT RAISE(ABORT,'queue unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	w := newWorker(t, d, svc)
	w.Poll(ctx)
	var marked, queued int
	_ = d.QueryRow(`SELECT COUNT(*) FROM single_use_tokens WHERE booked_notified_at IS NOT NULL OR expired_notified_at IS NOT NULL`).Scan(&marked)
	_ = d.QueryRow(`SELECT COUNT(*) FROM webhook_deliveries`).Scan(&queued)
	if marked != 0 || queued != 0 {
		t.Fatal("failed enqueue committed a marker or delivery")
	}
	if _, err = d.Exec(`DROP TRIGGER unavailable_queue`); err != nil {
		t.Fatal(err)
	}
	w.Poll(ctx)
	if len(counts) != 2 {
		t.Fatalf("recovery delivered %d notifications, expected booked and expired", len(counts))
	}
	for _, n := range counts {
		if n != 1 {
			t.Fatal("duplicate first attempt")
		}
	}
	w.Poll(ctx)
	for _, n := range counts {
		if n != 1 {
			t.Fatal("ignored retry delay")
		}
	}
	past := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	if _, err = d.Exec(`UPDATE jobs SET run_at=? WHERE status='pending'`, past); err != nil {
		t.Fatal(err)
	}
	newWorker(t, d, svc).Poll(ctx)
	for _, n := range counts {
		if n != 2 {
			t.Fatal("persisted retry was not delivered")
		}
	}
	newWorker(t, d, svc).Poll(ctx)
	deliveries, err := svc.ListDeliveries(ctx, "host-01", hook.ID)
	if err != nil || len(deliveries) != 2 {
		t.Fatalf("duplicates after restart: %v %v", deliveries, err)
	}
	for _, delivery := range deliveries {
		if delivery.Status != "success" || delivery.AttemptCount != 2 {
			t.Fatalf("unexpected terminal state: %+v", delivery)
		}
	}
	_ = d.QueryRow(`SELECT COUNT(*) FROM single_use_tokens WHERE id='used-token' AND expired_notified_at IS NOT NULL`).Scan(&marked)
	if marked != 0 {
		t.Fatal("booked link was later reported expired")
	}
}

func TestWebhookScopes_OverlapOnceAndScopeChangeBeforeDelivery(t *testing.T) {
	d, svc := setup(t)
	ctx := context.Background()
	for _, q := range []string{
		`UPDATE users SET is_admin=1 WHERE id='host-01'`,
		`INSERT INTO teams(id,name,slug)VALUES('scope-team','Scope team','scope-team')`,
		`INSERT INTO event_types(id,user_id,team_id,slug,name,duration_minutes)VALUES('scope-a','host-01','scope-team','scope-a','A',30),('scope-b','host-02','scope-team','scope-b','B',30),('scope-c','host-03',NULL,'scope-c','C',30)`,
		`INSERT INTO bookings(id,event_type_id,host_id,start_at,end_at)VALUES('booking-a','scope-a','host-01','2026-11-25T10:00:00Z','2026-11-25T10:30:00Z'),('booking-b','scope-b','host-02','2026-11-25T10:00:00Z','2026-11-25T10:30:00Z'),('booking-c','scope-c','host-03','2026-11-25T10:00:00Z','2026-11-25T10:30:00Z')`,
	} {
		if _, err := d.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	sent := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { sent++; w.WriteHeader(204) }))
	defer server.Close()
	scopes := []webhook.Scope{{Type: "team", ID: "scope-team"}, {Type: "event", ID: "scope-a"}}
	hook, _, err := svc.CreateConfigured(ctx, "host-01", server.URL, []string{"booking.created"}, webhook.Options{Scopes: &scopes})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b", "c"} {
		if err = svc.Enqueue(ctx, "booking.created", webhook.BookingPayload{ID: "booking-" + id, HostID: "host-01", EventTypeSlug: "scope-" + id}); err != nil {
			t.Fatal(err)
		}
	}
	deliveries, _ := svc.ListDeliveries(ctx, "host-01", hook.ID)
	if len(deliveries) != 2 {
		t.Fatalf("overlap produced duplicates or unrelated events: %d", len(deliveries))
	}
	scopes = []webhook.Scope{{Type: "user", ID: "host-01"}}
	if err = svc.UpdateConfig(ctx, "host-01", hook.ID, webhook.ConfigPatch{Scopes: &scopes}); err != nil {
		t.Fatal(err)
	}
	newWorker(t, d, svc).Poll(ctx)
	if sent != 1 {
		t.Fatalf("changed scope was not enforced on queued delivery: %d", sent)
	}
}

func TestSingleUseLifecycle_TeamMemberReceivesTheirOwnGeneratedLinks(t *testing.T) {
	d, svc := setup(t)
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO teams(id,name,slug)VALUES('link-team','Link team','link-team')`,
		`INSERT INTO team_members(team_id,user_id,role)VALUES('link-team','host-01','member'),('link-team','host-02','owner')`,
		`INSERT INTO event_types(id,user_id,team_id,slug,name,duration_minutes)VALUES('link-event','host-02','link-team','link-event','Team meeting',30)`,
		`INSERT INTO bookings(id,event_type_id,host_id,start_at,end_at)VALUES('team-booking','link-event','host-02','2026-11-25T10:00:00Z','2026-11-25T10:30:00Z')`,
		`INSERT INTO single_use_tokens(id,token,event_type_id,created_by_user_id,used_at,booking_id,created_at)VALUES('own-link','own-link','link-event','host-01','2026-11-20T00:00:00Z','team-booking','2026-11-19T00:00:00Z'),('other-link','other-link','link-event','host-02','2026-11-20T00:00:00Z','team-booking','2026-11-19T00:00:00Z')`,
	} {
		if _, err := d.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(raw), `"id":"own-link"`) {
			t.Error("received someone else's generated link")
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	scopes := []webhook.Scope{{Type: "user", ID: "host-01"}}
	if _, _, err := svc.CreateConfigured(ctx, "host-01", server.URL, []string{"single_use_link.booked"}, webhook.Options{Scopes: &scopes}); err != nil {
		t.Fatal(err)
	}
	newWorker(t, d, svc).Poll(ctx)
	if calls != 1 {
		t.Fatalf("expected the creator's one notification, got %d", calls)
	}
}
