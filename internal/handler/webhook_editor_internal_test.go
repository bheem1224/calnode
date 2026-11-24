package handler

import (
	"encoding/json"
	"testing"
)

func TestWebhookEditorAtomicConfigAndURLValidation(t *testing.T) {
	h, u, key := agentFixture(t)
	made := libraryRequest(h, key, "POST", "/v1/webhooks", `{"url":"https://93.184.216.34/old","events":["booking.created"],"fields":["id"],"scopes":[{"type":"user","id":"`+u.ID+`"}]}`, h.CreateWebhook, "")
	if made.Code != 201 {
		t.Fatal(made.Body.String())
	}
	var hook struct{ ID string }
	if err := json.Unmarshal(made.Body.Bytes(), &hook); err != nil {
		t.Fatal(err)
	}
	var originalSecret string
	if err := h.db.QueryRow(`SELECT secret_enc FROM webhooks WHERE id=?`, hook.ID).Scan(&originalSecret); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"", "file:///etc/passwd", "http://127.0.0.1/private", "http://169.254.169.254/latest/meta-data", "https://user:password@93.184.216.34/hook"} {
		body, _ := json.Marshal(map[string]any{"url": target, "is_active": false})
		bad := libraryRequest(h, key, "PATCH", "/v1/webhooks/"+hook.ID, string(body), h.PatchWebhook, hook.ID)
		if bad.Code != 400 {
			t.Fatalf("unsafe target %q: %d %s", target, bad.Code, bad.Body.String())
		}
	}
	var active bool
	if err := h.db.QueryRow(`SELECT is_active FROM webhooks WHERE id=?`, hook.ID).Scan(&active); err != nil || !active {
		t.Fatalf("invalid partial update mutated webhook: %v", err)
	}
	updated := libraryRequest(h, key, "PATCH", "/v1/webhooks/"+hook.ID, `{"url":"https://93.184.216.34/new","is_active":false,"events":["booking.cancelled"],"fields":[]}`, h.PatchWebhook, hook.ID)
	if updated.Code != 204 {
		t.Fatal(updated.Code, updated.Body.String())
	}
	var target, events, fields, secret string
	if err := h.db.QueryRow(`SELECT url,events,fields,is_active,secret_enc FROM webhooks WHERE id=?`, hook.ID).Scan(&target, &events, &fields, &active, &secret); err != nil {
		t.Fatal(err)
	}
	if target != "https://93.184.216.34/new" || active || events != `["booking.cancelled"]` || fields != "[]" || secret != originalSecret {
		t.Fatal("editor lost settings or rotated secret")
	}
	resumed := libraryRequest(h, key, "PATCH", "/v1/webhooks/"+hook.ID, `{"is_active":true}`, h.PatchWebhook, hook.ID)
	if resumed.Code != 204 {
		t.Fatal(resumed.Code, resumed.Body.String())
	}
}
