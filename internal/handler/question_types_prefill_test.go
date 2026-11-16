package handler_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestQuestionTypesAndPrefill(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	// 1. Create a hidden prefillable URL-identifier question + expanded types.
	qNumRec := httptest.NewRecorder()
	qNumReq := authReq(http.MethodPost, "/v1/event-types/"+slug+"/questions", `{
		"label": "Deal ID",
		"type": "number",
		"required": true,
		"identifier": "dealId",
		"hidden": true,
		"lock_if_prefilled": true
	}`, key)
	qNumReq.SetPathValue("slug", slug)
	h.RequireAuth(h.CreateQuestion)(qNumRec, qNumReq)
	if qNumRec.Code != http.StatusCreated {
		t.Fatalf("create number question: %d — %s", qNumRec.Code, qNumRec.Body.String())
	}
	numQ := mustCreated(t, qNumRec, "number question")
	numQID := mustString(t, numQ, "id", "number question")

	qMultiRec := httptest.NewRecorder()
	qMultiReq := authReq(http.MethodPost, "/v1/event-types/"+slug+"/questions", `{
		"label": "Topics",
		"type": "multiselect",
		"options": ["Pricing", "Demo", "Security"],
		"required": false
	}`, key)
	qMultiReq.SetPathValue("slug", slug)
	h.RequireAuth(h.CreateQuestion)(qMultiRec, qMultiReq)
	if qMultiRec.Code != http.StatusCreated {
		t.Fatalf("create multiselect question: %d — %s", qMultiRec.Code, qMultiRec.Body.String())
	}
	multiQID := mustString(t, mustCreated(t, qMultiRec, "multiselect question"), "id", "multiselect question")

	qEmailsRec := httptest.NewRecorder()
	qEmailsReq := authReq(http.MethodPost, "/v1/event-types/"+slug+"/questions", `{
		"label": "Additional Guests",
		"type": "multiple_emails",
		"required": false
	}`, key)
	qEmailsReq.SetPathValue("slug", slug)
	h.RequireAuth(h.CreateQuestion)(qEmailsRec, qEmailsReq)
	if qEmailsRec.Code != http.StatusCreated {
		t.Fatalf("create multiple_emails question: %d — %s", qEmailsRec.Code, qEmailsRec.Body.String())
	}
	emailsQID := mustString(t, mustCreated(t, qEmailsRec, "multiple_emails question"), "id", "multiple_emails question")

	// 2. BookPage renders identifier, hidden, and lock attributes.
	pageRec := httptest.NewRecorder()
	pageReq := httptest.NewRequest(http.MethodGet, "/book/"+slug, nil)
	pageReq.SetPathValue("slug", slug)
	h.BookPage(pageRec, pageReq)
	if pageRec.Code != http.StatusOK {
		t.Fatalf("BookPage: %d", pageRec.Code)
	}
	body := pageRec.Body.String()
	if !strings.Contains(body, `data-identifier="dealId"`) || !strings.Contains(body, `data-lock-if-prefilled="true"`) {
		t.Errorf("BookPage missing prefill/lock attributes")
	}

	// 3. Invalid number / multiselect / multiple_emails rejected on booking.
	badBody := fmt.Sprintf(`{
		"event_type_slug": %q,
		"start_at": "2026-06-20T09:00:00Z",
		"name": "Alice",
		"email": "alice@example.com",
		"answers": [{"question_id": %q, "value": "not-a-number"}]
	}`, slug, numQID)
	badRec := httptest.NewRecorder()
	badReq := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(badBody))
	badReq.Header.Set("Content-Type", "application/json")
	h.CreateBooking(badRec, badReq)
	if badRec.Code != http.StatusBadRequest {
		t.Errorf("invalid number answer: got %d, want 400", badRec.Code)
	}

	// 4. Valid answers across expanded types succeed.
	okBody := fmt.Sprintf(`{
		"event_type_slug": %q,
		"start_at": "2026-06-20T09:00:00Z",
		"name": "Alice",
		"email": "alice@example.com",
		"answers": [
			{"question_id": %q, "value": "42"},
			{"question_id": %q, "value": "Pricing, Security"},
			{"question_id": %q, "value": "a@example.com, b@example.com"}
		]
	}`, slug, numQID, multiQID, emailsQID)
	okRec := httptest.NewRecorder()
	okReq := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(okBody))
	okReq.Header.Set("Content-Type", "application/json")
	h.CreateBooking(okRec, okReq)
	if okRec.Code != http.StatusCreated {
		t.Fatalf("valid expanded answers: got %d — %s", okRec.Code, okRec.Body.String())
	}
}
