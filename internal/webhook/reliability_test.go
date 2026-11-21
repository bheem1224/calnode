package webhook_test

import (
	"context"
	"encoding/json"
	"github.com/calnode/calnode/internal/webhook"
	"testing"
	"time"
)

func TestExplicitEmptyFieldsAndDurableSourceIdentity(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for _, q := range []string{`INSERT INTO event_types(id,user_id,slug,name,duration_minutes)VALUES('e','user-test-01','e','Event',30)`, `INSERT INTO bookings(id,event_type_id,host_id,start_at,end_at)VALUES('b','e','user-test-01','2026-12-01T10:00:00Z','2026-12-01T10:30:00Z')`} {
		if _, err := e.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	hook, _, err := e.svc.CreateConfigured(ctx, testUserID, "https://example.test/hook", []string{"booking.created", "single_use_link.expired"}, webhook.Options{Fields: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	p := webhook.BookingPayload{ID: "b", HostID: testUserID, EventTypeSlug: "e", LocationValue: "private meeting URL", Status: "confirmed"}
	for range 2 {
		if err = e.svc.EnqueueEvent(ctx, "booking.created", p, "source-1"); err != nil {
			t.Fatal(err)
		}
	}
	if err = e.svc.EnqueueSingleUse(ctx, "single_use_link.expired", webhook.SingleUsePayload{ID: "link", Token: "private-token", EventTypeID: "e", EventTypeSlug: "e", HostID: testUserID}); err != nil {
		t.Fatal(err)
	}
	rows, err := e.db.Query(`SELECT payload,event_type_id,resource_id FROM webhook_deliveries WHERE webhook_id=?`, hook.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var raw, eventID, resource string
		if err = rows.Scan(&raw, &eventID, &resource); err != nil {
			t.Fatal(err)
		}
		var envelope struct{ Data map[string]any }
		if err = json.Unmarshal([]byte(raw), &envelope); err != nil {
			t.Fatal(err)
		}
		if len(envelope.Data) != 0 {
			t.Fatalf("explicit empty selection leaked data: %s", raw)
		}
		if eventID != "e" || resource == "" {
			t.Fatal("authorization depended on filtered payload")
		}
		count++
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("duplicate source produced %d deliveries", count)
	}
}
func TestSingleUseFieldsAndLegacyDefaultAreDistinct(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.db.Exec(`INSERT INTO event_types(id,user_id,slug,name,duration_minutes)VALUES('e','user-test-01','e','Event',30)`)
	selected, _, err := e.svc.CreateConfigured(ctx, testUserID, "https://example.test/hook", []string{"single_use_link.expired"}, webhook.Options{Fields: []string{webhook.FieldID, webhook.FieldExpiresAt}})
	if err != nil {
		t.Fatal(err)
	}
	legacy, _, err := e.svc.Create(ctx, testUserID, "https://example.test/legacy", []string{"single_use_link.expired"})
	if err != nil {
		t.Fatal(err)
	}
	err = e.svc.EnqueueSingleUse(ctx, "single_use_link.expired", webhook.SingleUsePayload{ID: "link", Token: "private-token", EventTypeID: "e", EventTypeSlug: "e", HostID: testUserID, ExpiresAt: "2026-01-01T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{selected.ID, legacy.ID} {
		var raw string
		e.db.QueryRow(`SELECT payload FROM webhook_deliveries WHERE webhook_id=?`, id).Scan(&raw)
		var payload struct{ Data map[string]any }
		if err = json.Unmarshal([]byte(raw), &payload); err != nil {
			t.Fatal(err)
		}
		_, hasToken := payload.Data["token"]
		if id == selected.ID && (len(payload.Data) != 2 || hasToken) {
			t.Fatal("selected fields ignored", raw)
		}
		if id == legacy.ID && !hasToken {
			t.Fatal("legacy default unexpectedly changed", raw)
		}
	}
}

func TestOutboxExpansionDoesNotRestartDeliveryWindow(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, _, err := e.svc.Create(ctx, testUserID, "https://example.test/hook", []string{"booking.created"}); err != nil {
		t.Fatal(err)
	}
	occurred := time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339Nano)
	if err := e.svc.EnqueueEvent(ctx, "booking.created", webhook.BookingPayload{HostID: testUserID, OccurredAt: occurred}, "delayed-source"); err != nil {
		t.Fatal(err)
	}
	var started string
	if err := e.db.QueryRow(`SELECT created_at FROM jobs WHERE type='webhook.deliver'`).Scan(&started); err != nil {
		t.Fatal(err)
	}
	if started != occurred {
		t.Fatalf("outbox expansion restarted retry window: got %s want %s", started, occurred)
	}
}
