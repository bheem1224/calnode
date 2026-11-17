package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmailTemplates_previewSanitizeAndLibrary(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	// 1. Default preview renders stylized HTML with Reschedule/Cancel links without sending email.
	rec := httptest.NewRecorder()
	req := authReq(http.MethodPost, "/v1/event-types/"+slug+"/email-preview",
		`{"type":"confirmation","mode":"note","subject":"Confirmed: {Event}","body":"See you {SchedulerFirst}!"}`, key)
	req.SetPathValue("slug", slug)
	h.RequireAuth(h.PreviewEmailTemplate)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview default: %d %s", rec.Code, rec.Body.String())
	}
	var prev struct {
		Subject     string `json:"subject"`
		HTML        string `json:"html"`
		DefaultHTML string `json:"default_html"`
	}
	json.Unmarshal(rec.Body.Bytes(), &prev)
	if !strings.HasPrefix(prev.Subject, "Confirmed:") {
		t.Errorf("unexpected subject: %q", prev.Subject)
	}
	if !strings.Contains(prev.HTML, "See you Alex!") || !strings.Contains(prev.DefaultHTML, "manage/sample-token") {
		t.Errorf("preview missing expected body or default manage link")
	}

	// 2. Custom HTML strips <script> and onclick= before returning/saving.
	rec = httptest.NewRecorder()
	req = authReq(http.MethodPost, "/v1/event-types/"+slug+"/email-preview",
		`{"type":"confirmation","mode":"html","body":"<div onclick=\"alert(1)\"><script>evil()</script><p>Hello {Scheduler}</p></div>"}`, key)
	req.SetPathValue("slug", slug)
	h.RequireAuth(h.PreviewEmailTemplate)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview html: %d %s", rec.Code, rec.Body.String())
	}
	json.Unmarshal(rec.Body.Bytes(), &prev)
	if strings.Contains(prev.HTML, "<script") || strings.Contains(prev.HTML, "onclick") || !strings.Contains(prev.HTML, "Hello Alex Johnson") {
		t.Fatalf("sanitization/interpolation failed: %s", prev.HTML)
	}

	// 3. Script-only HTML is rejected with 400.
	rec = httptest.NewRecorder()
	req = authReq(http.MethodPost, "/v1/event-types/"+slug+"/email-preview",
		`{"type":"confirmation","mode":"html","body":"<script>alert(1)</script>"}`, key)
	req.SetPathValue("slug", slug)
	h.RequireAuth(h.PreviewEmailTemplate)(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("script-only html: got %d; want 400", rec.Code)
	}

	// 4. Save reusable default template -> new event type inherits it.
	rec = httptest.NewRecorder()
	h.RequireAuth(h.CreateEmailTemplate)(rec, authReq(http.MethodPost, "/v1/email-templates",
		`{"name":"VIP Confirm","kind":"confirmation","is_default":true,"mode":"html","subject":"VIP: {Event}","body":"<div><p>Welcome {Scheduler}</p></div>"}`, key))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create template: %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.RequireAuth(h.CreateEventType)(rec, authReq(http.MethodPost, "/v1/event-types",
		`{"slug":"vip-call","name":"VIP Call","duration_minutes":30}`, key))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create event type: %d %s", rec.Code, rec.Body.String())
	}
	var et struct {
		SubjConfirmation *string `json:"subj_confirmation"`
		HTMLConfirmation *string `json:"html_confirmation"`
	}
	json.Unmarshal(rec.Body.Bytes(), &et)
	if et.SubjConfirmation == nil || *et.SubjConfirmation != "VIP: {Event}" {
		t.Errorf("expected inherited subj_confirmation, got %v", et.SubjConfirmation)
	}
	if et.HTMLConfirmation == nil || !strings.Contains(*et.HTMLConfirmation, "Welcome {Scheduler}") {
		t.Errorf("expected inherited html_confirmation, got %v", et.HTMLConfirmation)
	}
}
