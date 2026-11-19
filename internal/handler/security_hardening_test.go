package handler_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSingleUseLink_DoesNotExposeSlugAndWorksViaTokenEndpoints(t *testing.T) {
	h, _, apiKey, _ := setupWorkspaceWithDB(t)
	h.SetBaseURL("https://cal.example.com")

	slug, etID := seedEventTypeHTTP(t, h, apiKey)
	for i := 0; i < 7; i++ {
		seedAvailabilityWindow(t, h, apiKey, i, "08:00", "20:00")
	}

	// Create single-use link
	createReq := authReq(http.MethodPost, "/v1/event-types/"+etID+"/single-use-links", `{"expires_in_hours": 48}`, apiKey)
	createReq.SetPathValue("id", etID)
	createRec := httptest.NewRecorder()
	h.RequireAuth(h.CreateSingleUseLink)(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("CreateSingleUseLink: %d — %s", createRec.Code, createRec.Body.String())
	}
	var created struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(createRec.Body.Bytes(), &created)

	// GET /t/{token} must NOT expose the underlying event slug anywhere in HTML/JS.
	pageReq := httptest.NewRequest(http.MethodGet, "/t/"+created.Token, nil)
	pageReq.SetPathValue("token", created.Token)
	pageRec := httptest.NewRecorder()
	h.SingleUseBookPage(pageRec, pageReq)
	if pageRec.Code != http.StatusOK {
		t.Fatalf("SingleUseBookPage: %d", pageRec.Code)
	}
	if strings.Contains(pageRec.Body.String(), slug) {
		t.Errorf("GET /t/{token} leaked underlying event slug %q in HTML", slug)
	}

	// GET /v1/t/{token}/slots works without slug.
	targetDay := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, 5)
	dayStr := targetDay.Format("2006-01-02")
	slotsReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/v1/t/%s/slots?from=%s&to=%s&tz=UTC", created.Token, dayStr, dayStr), nil)
	slotsReq.SetPathValue("token", created.Token)
	slotsRec := httptest.NewRecorder()
	h.GetSingleUseSlots(slotsRec, slotsReq)
	if slotsRec.Code != http.StatusOK {
		t.Fatalf("GetSingleUseSlots: %d — %s", slotsRec.Code, slotsRec.Body.String())
	}

	// POST /v1/t/{token}/bookings works without event_type_slug and persists phone, notes, guests.
	slotStart := time.Date(targetDay.Year(), targetDay.Month(), targetDay.Day(), 10, 0, 0, 0, time.UTC)
	bookReq := httptest.NewRequest(http.MethodPost, "/v1/t/"+created.Token+"/bookings", strings.NewReader(fmt.Sprintf(`{
		"start_at": %q,
		"name": "Founder One",
		"email": "founder@acme.vc",
		"contact_phone": "+1 555-010-2000",
		"notes": "Discussing Series A",
		"guests": ["cofounder@acme.vc", "cofounder@acme.vc", "advisor@acme.vc"],
		"timezone": "UTC"
	}`, slotStart.Format(time.RFC3339))))
	bookReq.Header.Set("Content-Type", "application/json")
	bookReq.SetPathValue("token", created.Token)
	bookRec := httptest.NewRecorder()
	h.CreateSingleUseBooking(bookRec, bookReq)
	if bookRec.Code != http.StatusCreated {
		t.Fatalf("CreateSingleUseBooking: %d — %s", bookRec.Code, bookRec.Body.String())
	}
	var booked struct {
		ID           string   `json:"id"`
		ContactPhone string   `json:"contact_phone"`
		Notes        string   `json:"notes"`
		Guests       []string `json:"guests"`
	}
	if err := json.Unmarshal(bookRec.Body.Bytes(), &booked); err != nil {
		t.Fatalf("decode booking: %v", err)
	}
	if booked.ContactPhone != "+1 555-010-2000" || booked.Notes != "Discussing Series A" || len(booked.Guests) != 2 {
		t.Errorf("unexpected persisted fields: %+v", booked)
	}
}

func TestCreateBooking_RejectsMoreThan10Guests(t *testing.T) {
	h, _, apiKey, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, apiKey)
	for i := 0; i < 7; i++ {
		seedAvailabilityWindow(t, h, apiKey, i, "08:00", "20:00")
	}
	targetDay := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, 5)
	slotStart := time.Date(targetDay.Year(), targetDay.Month(), targetDay.Day(), 11, 0, 0, 0, time.UTC)

	guests := make([]string, 11)
	for i := range guests {
		guests[i] = fmt.Sprintf("g%d@example.com", i)
	}
	guestsJSON, _ := json.Marshal(guests)
	req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(fmt.Sprintf(`{
		"event_type_slug": %q,
		"start_at": %q,
		"name": "Spam Test",
		"email": "spam@example.com",
		"guests": %s,
		"timezone": "UTC"
	}`, slug, slotStart.Format(time.RFC3339), string(guestsJSON))))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateBooking(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for >10 guests, got %d — %s", rec.Code, rec.Body.String())
	}
}

func TestPatchTeam_ValidatesLogoURLAndBrandColor(t *testing.T) {
	h, _, apiKey, _ := setupWorkspaceWithDB(t)

	// Create team
	cReq := authReq(http.MethodPost, "/v1/teams", `{"name":"Sec Team"}`, apiKey)
	cRec := httptest.NewRecorder()
	h.RequireAuth(h.CreateTeam)(cRec, cReq)
	if cRec.Code != http.StatusCreated {
		t.Fatalf("CreateTeam: %d — %s", cRec.Code, cRec.Body.String())
	}
	var team struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(cRec.Body.Bytes(), &team)

	for _, badBody := range []string{
		`{"logo_url":"javascript:alert(1)"}`,
		`{"logo_url":"https://evil.example/px.png?leak=secret"}`,
		`{"logo_url":"http://insecure.example/logo.png"}`,
		`{"brand_color":"red; background:url(https://evil.example)"}`,
	} {
		pReq := authReq(http.MethodPatch, "/v1/teams/"+team.ID, badBody, apiKey)
		pReq.SetPathValue("id", team.ID)
		pRec := httptest.NewRecorder()
		h.RequireAuth(h.PatchTeam)(pRec, pReq)
		if pRec.Code != http.StatusBadRequest {
			t.Errorf("PatchTeam(%s): got %d, want 400", badBody, pRec.Code)
		}
	}

	okReq := authReq(http.MethodPatch, "/v1/teams/"+team.ID, `{"logo_url":"https://cdn.example.com/logo.png","brand_color":"#2563eb"}`, apiKey)
	okReq.SetPathValue("id", team.ID)
	okRec := httptest.NewRecorder()
	h.RequireAuth(h.PatchTeam)(okRec, okReq)
	if okRec.Code != http.StatusOK {
		t.Errorf("PatchTeam valid payload: got %d — %s", okRec.Code, okRec.Body.String())
	}
}

func TestHiddenGuestsCannotBeSubmittedThroughPublicOrTokenAPI(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)
	slug, id := seedEventTypeHTTP(t, h, key)
	for day := 0; day < 7; day++ {
		seedAvailabilityWindow(t, h, key, day, "08:00", "20:00")
	}
	if _, err := database.Exec(`UPDATE event_types SET booking_form_config='[{"key":"guests","hidden":true}]' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	tokenReq := authReq("POST", "/", `{}`, key)
	tokenReq.SetPathValue("id", id)
	tokenRec := httptest.NewRecorder()
	h.RequireAuth(h.CreateSingleUseLink)(tokenRec, tokenReq)
	if tokenRec.Code != 201 {
		t.Fatal(tokenRec.Code, tokenRec.Body)
	}
	var link struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(tokenRec.Body.Bytes(), &link); err != nil {
		t.Fatal(err)
	}
	day := time.Now().UTC().AddDate(0, 0, 5)
	start := time.Date(day.Year(), day.Month(), day.Day(), 10, 0, 0, 0, time.UTC)
	body := fmt.Sprintf(`{"event_type_slug":%q,"start_at":%q,"name":"Booker","email":"booker@example.com","guests":["unwanted@example.com"]}`, slug, start.Format(time.RFC3339))
	for _, viaToken := range []bool{false, true} {
		req := httptest.NewRequest("POST", "/", strings.NewReader(body))
		rec := httptest.NewRecorder()
		if viaToken {
			req.SetPathValue("token", link.Token)
			h.CreateSingleUseBooking(rec, req)
		} else {
			h.CreateBooking(rec, req)
		}
		if rec.Code != 400 {
			t.Fatalf("via token %v: %d %s", viaToken, rec.Code, rec.Body)
		}
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM bookings`).Scan(&count); err != nil || count != 0 {
		t.Fatal("rejected request created booking", count, err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM single_use_tokens WHERE token=? AND used_at IS NULL`, link.Token).Scan(&count); err != nil || count != 1 {
		t.Fatal("rejected request burned token", count, err)
	}
}
