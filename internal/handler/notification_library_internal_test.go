package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/access"
)

func libraryRequest(h *Handler, key, method, path, body string, fn http.HandlerFunc, id string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("X-API-Key", key)
	r.SetPathValue("id", id)
	w := httptest.NewRecorder()
	h.RequireAuth(fn)(w, r)
	return w
}
func TestEmailLibrary_PersonalCRUDPreviewAndDefaultIsolation(t *testing.T) {
	h, u, key := agentFixture(t)
	// Template management works without an event type or event-management grant.
	narrow := access.Grants{"profile.read": "own", "profile.update": "own"}
	if _, err := h.db.Exec(`UPDATE api_keys SET permissions_json=? WHERE user_id=?`, narrow.JSON(), u.ID); err != nil {
		t.Fatal(err)
	}
	preview := libraryRequest(h, key, "POST", "/v1/email-templates/preview", `{"type":"confirmation","mode":"html","body":"<p>Hello {Scheduler}</p><script>bad()</script>"}`, h.PreviewSavedEmailTemplate, "")
	if preview.Code != 200 || strings.Contains(preview.Body.String(), "<script") {
		t.Fatalf("generic preview: %d %s", preview.Code, preview.Body.String())
	}
	create := libraryRequest(h, key, "POST", "/v1/email-templates", `{"name":"First","kind":"confirmation","mode":"note","body":"Original copy","is_default":true}`, h.CreateEmailTemplate, "")
	if create.Code != 201 {
		t.Fatal(create.Body.String())
	}
	var first emailTemplateJSON
	_ = json.Unmarshal(create.Body.Bytes(), &first)
	if _, _, err := h.callAgentOperation(context.Background(), u, personalEventOp("saved-copy")); err != nil {
		t.Fatal(err)
	}
	update := libraryRequest(h, key, "PATCH", "/v1/email-templates/"+first.ID, `{"name":"Updated","kind":"confirmation","mode":"html","body":"<p>New copy</p><script>bad()</script>","subject":"Hello {Event}","is_default":true}`, h.UpdateEmailTemplate, first.ID)
	if update.Code != 200 {
		t.Fatal(update.Body.String())
	}
	var note string
	_ = h.db.QueryRow(`SELECT msg_confirmation FROM event_types WHERE slug='saved-copy'`).Scan(&note)
	if note != "Original copy" {
		t.Fatal("editing the library retroactively changed an event")
	}
	if _, _, err := h.callAgentOperation(context.Background(), u, personalEventOp("new-copy")); err != nil {
		t.Fatal(err)
	}
	var html string
	_ = h.db.QueryRow(`SELECT html_confirmation FROM event_types WHERE slug='new-copy'`).Scan(&html)
	if !strings.Contains(html, "New copy") || strings.Contains(html, "script") {
		t.Fatal("new event did not inherit sanitized default")
	}
	second := libraryRequest(h, key, "POST", "/v1/email-templates", `{"name":"Second","kind":"confirmation","mode":"note","body":"Other","is_default":true}`, h.CreateEmailTemplate, "")
	if second.Code != 201 {
		t.Fatal(second.Body.String())
	}
	var defaults int
	_ = h.db.QueryRow(`SELECT COUNT(*) FROM email_templates WHERE user_id=? AND kind='confirmation' AND is_default=1`, u.ID).Scan(&defaults)
	if defaults != 1 {
		t.Fatal("multiple defaults")
	}
	if _, err := h.db.Exec(`INSERT INTO users(id,name,email)VALUES('other','Other','other@example.test')`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Exec(`INSERT INTO email_templates(id,user_id,name,kind,mode,body,created_at,updated_at)VALUES('foreign','other','Private','confirmation','note','Secret','2026-11-20T00:00:00Z','2026-11-20T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	foreign := libraryRequest(h, key, "PATCH", "/v1/email-templates/foreign", `{"name":"Stolen","kind":"confirmation","mode":"note"}`, h.UpdateEmailTemplate, "foreign")
	if foreign.Code != 404 {
		t.Fatal("edited someone else's template")
	}
	removed := libraryRequest(h, key, "DELETE", "/v1/email-templates/"+first.ID, "", h.DeleteEmailTemplate, first.ID)
	if removed.Code != 200 {
		t.Fatal(removed.Body.String())
	}
}

func TestWebhookScopes_APIClampsCredentialsAndSavedCeiling(t *testing.T) {
	h, u, key := agentFixture(t)
	grants := access.Grants{"webhooks.create": "own", "webhooks.read": "own", "webhooks.update": "own", "bookings.read": "own"}
	if _, err := h.db.Exec(`UPDATE api_keys SET permissions_json=? WHERE user_id=?`, grants.JSON(), u.ID); err != nil {
		t.Fatal(err)
	}
	bad := libraryRequest(h, key, "POST", "/v1/webhooks", `{"url":"https://93.184.216.34/hook","events":["booking.created"],"scopes":[{"type":"instance"}]}`, h.CreateWebhook, "")
	if bad.Code != 403 {
		t.Fatalf("owner's narrowed credential bypassed scope: %d %s", bad.Code, bad.Body.String())
	}
	own := `{"url":"https://93.184.216.34/hook","event_type_id":"","events":["booking.created"],"scopes":[{"type":"user","id":"` + u.ID + `"}]}`
	made := libraryRequest(h, key, "POST", "/v1/webhooks", own, h.CreateWebhook, "")
	if made.Code != 201 {
		t.Fatalf("own scope: %d %s", made.Code, made.Body.String())
	}
	var hook struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
	}
	_ = json.Unmarshal(made.Body.Bytes(), &hook)
	if len(hook.Secret) != 64 {
		t.Fatal("signing secret not returned")
	}
	options := libraryRequest(h, key, "GET", "/v1/webhooks/scopes", "", h.WebhookScopeOptions, "")
	if options.Code != 200 || strings.Contains(options.Body.String(), `"type":"instance"`) {
		t.Fatal("offered ungrantable instance scope")
	}
	if _, err := h.db.Exec(`UPDATE api_keys SET permissions_json=? WHERE user_id=?`, access.Full().JSON(), u.ID); err != nil {
		t.Fatal(err)
	}
	expanded := libraryRequest(h, key, "PATCH", "/v1/webhooks/"+hook.ID, `{"scopes":[{"type":"instance"}]}`, h.PatchWebhook, hook.ID)
	if expanded.Code != 403 {
		t.Fatal("editing expanded a saved webhook's permission ceiling")
	}
}

func TestSingleUseAPI_ReadOnlyCannotReadPrivateLinks(t *testing.T) {
	h, u, key := agentFixture(t)
	if _, _, err := h.callAgentOperation(context.Background(), u, personalEventOp("private-link")); err != nil {
		t.Fatal(err)
	}
	made := libraryRequest(h, key, "POST", "/v1/event-types/private-link/single-use-links", `{"expires_in_hours":72}`, h.CreateSingleUseLink, "private-link")
	if made.Code != 201 {
		t.Fatal(made.Body.String())
	}
	var link singleUseLinkJSON
	if json.Unmarshal(made.Body.Bytes(), &link) != nil || link.ExpiresAt == nil {
		t.Fatal("missing link expiry")
	}
	expiry, err := time.Parse(time.RFC3339Nano, *link.ExpiresAt)
	if err != nil || expiry.Sub(time.Now()) < 71*time.Hour || expiry.Sub(time.Now()) > 73*time.Hour {
		t.Fatal("three-day expiry ignored")
	}
	if _, err = h.db.Exec(`UPDATE api_keys SET permissions_json=? WHERE user_id=?`, access.Grants{"event_types.read": "own"}.JSON(), u.ID); err != nil {
		t.Fatal(err)
	}
	listed := libraryRequest(h, key, "GET", "/v1/event-types/private-link/single-use-links", "", h.ListSingleUseLinks, "private-link")
	if listed.Code != 403 || strings.Contains(listed.Body.String(), link.Token) {
		t.Fatal("read-only access exposed a private token")
	}
}

func TestEmailLibrary_SavedHTMLReachesCancellationAndRescheduleMail(t *testing.T) {
	for _, kind := range []string{"cancellation", "reschedule"} {
		t.Run(kind, func(t *testing.T) {
			f := newInviteFixture(t, true)
			b, eventID := f.calnodeBooking(t)
			column := "html_cancellation"
			if kind == "reschedule" {
				column = "html_reschedule"
			}
			if _, err := f.h.db.Exec(`UPDATE event_types SET `+column+`='<p>Library lifecycle body {Event}</p>',reply_to='support@example.test' WHERE id=?`, eventID); err != nil {
				t.Fatal(err)
			}
			if kind == "cancellation" {
				if err := f.h.bookingSvc.CancelByID(context.Background(), b.ID, "test"); err != nil {
					t.Fatal(err)
				}
				updated, err := f.h.bookingSvc.Get(context.Background(), b.ID)
				if err != nil {
					t.Fatal(err)
				}
				f.h.cancelSideEffects(*updated)
			} else {
				newStart := b.StartAt.Add(24 * time.Hour)
				updated, err := f.h.bookingSvc.Reschedule(context.Background(), b.ID, newStart, newStart.Add(30*time.Minute))
				if err != nil {
					t.Fatal(err)
				}
				f.h.rescheduleSideEffects(*updated, eventID, b.StartAt, b.EndAt)
			}
			message := f.waitFor(t, inviteBookerEmail, 2)
			if !strings.Contains(message.HTML, "Library lifecycle body") || message.ReplyTo != "support@example.test" {
				t.Fatalf("saved HTML or reply-to was ignored: %+v", message)
			}
		})
	}
}
