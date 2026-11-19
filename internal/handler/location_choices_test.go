package handler_test

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOneAdditionalLocationOnHostedEmbedAndManage(t *testing.T) {
	h, database, key, user := setupWorkspaceWithDB(t)
	seedFullAvailabilityDB(t, database, user)
	slug, id := seedEventTypeHTTP(t, h, key)
	const meet = "https://meet.google.com/abc-defg-hij"
	const teams = "https://teams.microsoft.com/l/meetup-join/test"
	if _, err := database.Exec(`UPDATE event_types SET location_type='google_meet',location_value=?,location_options=? WHERE id=?`, meet, `[{"type":"teams","value":"`+teams+`"}]`, id); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/book/"+slug, nil)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.BookPage(rec, req)
	for _, text := range []string{`id="f-location-select"`, `name="selected_location" required`, `data-type="google_meet"`, `data-type="teams"`, `Google Meet`, `Microsoft Teams`} {
		if !strings.Contains(rec.Body.String(), text) {
			t.Errorf("hosted picker missing %q", text)
		}
	}
	req = httptest.NewRequest("GET", "/v1/event-types/"+slug+"/public", nil)
	req.SetPathValue("slug", slug)
	rec = httptest.NewRecorder()
	h.PublicEventType(rec, req)
	if strings.Contains(rec.Body.String(), meet) || strings.Contains(rec.Body.String(), teams) {
		t.Fatal("public choices disclosed meeting URLs before booking")
	}
	var metadata struct {
		Options []struct {
			Type, Value, Label string
			ID                 int
		} `json:"location_options"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &metadata); err != nil {
		t.Fatal(err)
	}
	if len(metadata.Options) != 2 || metadata.Options[0].Type != "google_meet" || metadata.Options[1].Value != "" || metadata.Options[1].ID != 1 {
		t.Fatalf("embed choices %+v", metadata.Options)
	}
	body := fmt.Sprintf(`{"event_type_slug":%q,"start_at":"2026-06-20T09:00:00Z","name":"Alice","email":"alice@example.com","selected_location":{"type":"teams","id":1}}`, slug)
	rec = httptest.NewRecorder()
	h.CreateBooking(rec, httptest.NewRequest("POST", "/v1/bookings", strings.NewReader(body)))
	booked := mustCreated(t, rec, "Teams booking")
	if booked["location_type"] != "teams" || booked["location_value"] != teams {
		t.Fatalf("wrong booked location: %v", booked)
	}
	manageURL := mustString(t, booked, "manage_url", "booking")
	token := manageURL[strings.LastIndex(manageURL, "/")+1:]
	req = httptest.NewRequest("GET", manageURL, nil)
	req.SetPathValue("token", token)
	rec = httptest.NewRecorder()
	h.ManagePage(rec, req)
	if !strings.Contains(rec.Body.String(), "Microsoft Teams") {
		t.Fatal("manage page lost selected provider", rec.Body)
	}
	req = httptest.NewRequest("POST", "/manage/"+token+"/reschedule", strings.NewReader(`{"start_at":"2026-06-20T10:00:00Z"}`))
	req.SetPathValue("token", token)
	rec = httptest.NewRecorder()
	h.RescheduleByToken(rec, req)
	if rec.Code != 200 {
		t.Fatalf("reschedule: %d %s", rec.Code, rec.Body)
	}
	var kind, value string
	if err := database.QueryRow(`SELECT location_type,location_value FROM bookings WHERE id=?`, booked["id"]).Scan(&kind, &value); err != nil || kind != "teams" || value != teams {
		t.Fatal("reschedule changed chosen location", kind, value, err)
	}
}

func TestAdditionalLocationsValidateChangesAndRejectInjection(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)
	slug, id := seedEventTypeHTTP(t, h, key)
	patch := func(body string) *httptest.ResponseRecorder {
		req := authReq("PATCH", "/v1/event-types/"+slug, body, key)
		req.SetPathValue("slug", slug)
		rec := httptest.NewRecorder()
		h.RequireAuth(h.PatchEventType)(rec, req)
		return rec
	}
	if rec := patch(`{"location_options":[{"type":"link","value":"javascript:alert(1)"}]}`); rec.Code != 400 {
		t.Fatal("unsafe URL saved", rec.Code, rec.Body)
	}
	// A disconnected provider must not prevent saving an unchanged existing option.
	if _, err := database.Exec(`UPDATE event_types SET location_options='[{"type":"teams","value":""}]' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if rec := patch(`{"name":"Renamed","location_options":[{"type":"teams","value":""}]}`); rec.Code != 200 {
		t.Fatal("unchanged option invalidated editor save", rec.Code, rec.Body)
	}
}
