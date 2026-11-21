package handler

import (
	"context"
	"github.com/calnode/calnode/internal/access"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebhookRetryPreservesSharedCredentialScopeAndLiveRevocation(t *testing.T) {
	h := rbacFixture(t)
	grants := access.Grants{"webhooks.create": "own", "webhooks.update": "own", "webhooks.deliveries": "own", "bookings.read": "own"}
	rbacActor(t, h, "hook-owner", grants, "", "")
	rbacActor(t, h, "outsider", grants, "", "")
	rbacExec(t, h, `INSERT INTO bookings(id,event_type_id,host_id,start_at,end_at)VALUES('hook-booking','personal','hook-owner','2026-12-01T10:00:00Z','2026-12-01T10:30:00Z')`)
	hook, _, err := h.webhookSvc.Create(context.Background(), "hook-owner", "https://example.test/hook", []string{"booking.created"})
	if err != nil {
		t.Fatal(err)
	}
	rbacExec(t, h, `INSERT INTO webhook_deliveries(id,webhook_id,booking_id,event,payload,status,event_type_id,resource_id)VALUES('d',?,'hook-booking','booking.created','{"data":{}}','failed','personal','hook-booking')`, hook.ID)
	rbacExec(t, h, `INSERT INTO jobs(id,type,payload,status,run_at)VALUES('delivery-job','webhook.deliver','{"webhook_delivery_id":"d"}','failed','2026-11-01T00:00:00Z')`)
	call := func(actor, channel string) int {
		r := httptest.NewRequest("POST", "/v1/webhooks/"+hook.ID+"/deliveries/d/retry", strings.NewReader(`{}`))
		r.SetPathValue("id", hook.ID)
		r.SetPathValue("deliveryId", "d")
		if channel == "key" {
			r.Header.Set("X-API-Key", actor+"-key-secret")
		} else {
			r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: actor + "-session"})
		}
		w := httptest.NewRecorder()
		rbacEndpoint(t, h, channel, actor, h.RetryWebhookDelivery, w, r)
		return w.Code
	}
	for _, channel := range []string{"session", "key", "mcp"} {
		if code := call("outsider", channel); code != 403 {
			t.Fatalf("foreign retry %s=%d", channel, code)
		}
		if code := call("hook-owner", channel); code != 200 {
			t.Fatalf("authorized retry %s=%d", channel, code)
		}
		if code := call("hook-owner", channel); code != 409 {
			t.Fatalf("active delivery retried twice: %d", code)
		}
		rbacExec(t, h, `UPDATE jobs SET status='failed' WHERE id='delivery-job'`)
		rbacExec(t, h, `UPDATE webhook_deliveries SET status='failed' WHERE id='d'`)
	}
	rbacExec(t, h, `UPDATE roles SET permissions_json='{}' WHERE id='hook-owner'`)
	if code := call("hook-owner", "key"); code != 403 {
		t.Fatalf("revoked credential replayed delivery: %d", code)
	}
}
