package handler_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEventTypeOverrides_validationAndBookPage(t *testing.T) {
	h, _, apiKey, _ := setupWorkspaceWithDB(t)

	etRec := httptest.NewRecorder()
	etReq := authReq(http.MethodPost, "/v1/event-types", `{
		"slug": "override-et",
		"name": "Override ET",
		"duration_minutes": 30,
		"location_type": "in_person"
	}`, apiKey)
	h.RequireAuth(h.CreateEventType)(etRec, etReq)
	if etRec.Code != http.StatusCreated {
		t.Fatalf("create event type: %d — %s", etRec.Code, etRec.Body.String())
	}

	// Reject javascript: redirect_url.
	badRedirRec := httptest.NewRecorder()
	badRedirReq := authReq(http.MethodPatch, "/v1/event-types/override-et", `{"redirect_url":"javascript:alert(1)"}`, apiKey)
	badRedirReq.SetPathValue("slug", "override-et")
	h.RequireAuth(h.PatchEventType)(badRedirRec, badRedirReq)
	if badRedirRec.Code != http.StatusBadRequest {
		t.Errorf("javascript redirect_url: status = %d; want 400", badRedirRec.Code)
	}

	// Reject invalid reply_to.
	badReplyRec := httptest.NewRecorder()
	badReplyReq := authReq(http.MethodPatch, "/v1/event-types/override-et", `{"reply_to":"not-an-email"}`, apiKey)
	badReplyReq.SetPathValue("slug", "override-et")
	h.RequireAuth(h.PatchEventType)(badReplyRec, badReplyReq)
	if badReplyRec.Code != http.StatusBadRequest {
		t.Errorf("invalid reply_to: status = %d; want 400", badReplyRec.Code)
	}

	// Accept valid overrides.
	okRec := httptest.NewRecorder()
	okReq := authReq(http.MethodPatch, "/v1/event-types/override-et", `{
		"calendar_event_title": "Intro: {Host} + {Scheduler}",
		"redirect_url": "https://example.com/thanks",
		"reply_to": "reply@example.com"
	}`, apiKey)
	okReq.SetPathValue("slug", "override-et")
	h.RequireAuth(h.PatchEventType)(okRec, okReq)
	if okRec.Code != http.StatusOK {
		t.Fatalf("valid overrides patch: %d — %s", okRec.Code, okRec.Body.String())
	}

	// BookPage embeds REDIRECT_URL.
	pageRec := httptest.NewRecorder()
	pageReq := httptest.NewRequest(http.MethodGet, "/book/override-et", nil)
	pageReq.SetPathValue("slug", "override-et")
	h.BookPage(pageRec, pageReq)
	if pageRec.Code != http.StatusOK {
		t.Fatalf("BookPage: %d", pageRec.Code)
	}
	body := pageRec.Body.String()
	if !strings.Contains(body, "https://example.com/thanks") && !strings.Contains(body, `https:\/\/example.com\/thanks`) {
		t.Errorf("BookPage missing configured redirect_url")
	}
}
