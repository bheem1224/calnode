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

func TestSingleUseSavedPrefillRecoversRequiredAnswersWithoutURLQuery(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)
	slug, eventID := seedEventTypeHTTP(t, h, key)
	_, err := database.Exec(`INSERT INTO event_type_questions(id,event_type_id,label,type,required,identifier,hidden) VALUES('reference',?,'Reference','text',1,'Reference',1)`, eventID)
	if err != nil {
		t.Fatal(err)
	}
	for day := 0; day < 7; day++ {
		seedAvailabilityWindow(t, h, key, day, "08:00", "20:00")
	}
	create := func(body string) *httptest.ResponseRecorder {
		r := authReq("POST", "/v1/event-types/"+eventID+"/single-use-links", body, key)
		r.SetPathValue("id", eventID)
		w := httptest.NewRecorder()
		h.RequireAuth(h.CreateSingleUseLink)(w, r)
		return w
	}
	secret := `</script><script>alert("unsafe")</script>`
	encoded, _ := json.Marshal(map[string]any{"prefill": map[string]string{"name": "Guest", "email": "guest@example.test", "Reference": secret}})
	w := create(string(encoded))
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	var link struct {
		ID, Token  string
		HasPrefill bool `json:"has_prefill"`
	}
	if json.Unmarshal(w.Body.Bytes(), &link) != nil || !link.HasPrefill {
		t.Fatal(w.Body)
	}
	if strings.Contains(w.Body.String(), "guest@example.test") {
		t.Fatal("creation response unnecessarily echoed recipient data")
	}
	r := httptest.NewRequest("GET", "/t/"+link.Token, nil)
	r.SetPathValue("token", link.Token)
	page := httptest.NewRecorder()
	h.SingleUseBookPage(page, r)
	if page.Code != 200 || !strings.Contains(page.Body.String(), `const savedPrefills = {`) || strings.Contains(page.Body.String(), secret) {
		t.Fatal("saved defaults missing or script injection possible", page.Code)
	}
	if page.Header().Get("Cache-Control") != "private, no-store" || page.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("recipient defaults may be cached or leaked via referrer")
	}
	listReq := authReq("GET", "/v1/event-types/"+eventID+"/single-use-links", "", key)
	listReq.SetPathValue("id", eventID)
	list := httptest.NewRecorder()
	h.RequireAuth(h.ListSingleUseLinks)(list, listReq)
	if list.Code != 200 || strings.Contains(list.Body.String(), "guest@example.test") || strings.Contains(list.Body.String(), "unsafe") {
		t.Fatal("link listing disclosed saved form data")
	}
	day := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, 10).Add(14 * time.Hour)
	body := fmt.Sprintf(`{"event_type_slug":%q,"single_use_token":%q,"name":"Guest","email":"guest@example.test","timezone":"UTC","start_at":%q}`, slug, link.Token, day.Format(time.RFC3339))
	book := httptest.NewRecorder()
	h.CreateBooking(book, httptest.NewRequest("POST", "/v1/bookings", strings.NewReader(body)))
	if book.Code != 201 {
		t.Fatal("required hidden answer did not recover from saved token", book.Code, book.Body)
	}
	var got string
	if err = database.QueryRow(`SELECT value FROM booking_answers WHERE question_id='reference'`).Scan(&got); err != nil || got != secret {
		t.Fatal("saved answer did not pass normal validation and persist", err)
	}
	replay := httptest.NewRecorder()
	h.CreateBooking(replay, httptest.NewRequest("POST", "/v1/bookings", strings.NewReader(body)))
	if replay.Code != 410 {
		t.Fatal("saved defaults bypassed token consumption", replay.Code, replay.Body)
	}
	// Prefills remain defaults: an explicit submitted answer is not overwritten.
	next := create(string(encoded))
	var nextLink struct{ Token string }
	if json.Unmarshal(next.Body.Bytes(), &nextLink) != nil || next.Code != 201 {
		t.Fatal(next.Code, next.Body)
	}
	nextBody := fmt.Sprintf(`{"event_type_slug":%q,"single_use_token":%q,"name":"Another Guest","email":"another@example.test","start_at":%q,"answers":[{"question_id":"reference","value":"explicit answer"}]}`, slug, nextLink.Token, day.Add(time.Hour).Format(time.RFC3339))
	nextBooking := httptest.NewRecorder()
	h.CreateBooking(nextBooking, httptest.NewRequest("POST", "/v1/bookings", strings.NewReader(nextBody)))
	if nextBooking.Code != 201 {
		t.Fatal(nextBooking.Code, nextBooking.Body)
	}
	var id struct{ ID string }
	if json.Unmarshal(nextBooking.Body.Bytes(), &id) != nil {
		t.Fatal(nextBooking.Body)
	}
	if err = database.QueryRow(`SELECT value FROM booking_answers WHERE booking_id=? AND question_id='reference'`, id.ID).Scan(&got); err != nil || got != "explicit answer" {
		t.Fatal("submitted answer was overwritten", err)
	}
	answersRequest := authReq("GET", "/v1/bookings/"+id.ID+"/answers", "", key)
	answersRequest.SetPathValue("id", id.ID)
	answers := httptest.NewRecorder()
	h.RequireAuth(h.GetBookingAnswers)(answers, answersRequest)
	if answers.Code != 200 || !strings.Contains(answers.Body.String(), `"identifier":"Reference"`) {
		t.Fatal("authorized answer omitted stable identifier", answers.Code, answers.Body)
	}
	for _, bad := range []string{`{"prefill":{"unknown":"value"}}`, `{"prefill":{"Reference":" "}}`, `{"prefill":{"Reference":123}}`} {
		if rec := create(bad); rec.Code != 400 {
			t.Fatal("invalid defaults accepted", rec.Code)
		}
	}
}

func TestSingleUseSavedPrefillNeverAuthorizesAnotherEvent(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)
	slug, eventID := seedEventTypeHTTP(t, h, key)
	_, err := database.Exec(`INSERT INTO event_type_questions(id,event_type_id,label,type,required,identifier,hidden) VALUES('reference',?,'Reference','text',1,'Reference',1)`, eventID)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 7; i++ {
		seedAvailabilityWindow(t, h, key, i, "08:00", "20:00")
	}
	_, err = database.Exec(`INSERT INTO single_use_tokens(id,token,event_type_id,created_by_user_id,prefill_json,expires_at,created_at) VALUES('expired','expired',?,(SELECT user_id FROM event_types WHERE id=?),'{"Reference":"saved"}','2000-01-01T00:00:00Z',CURRENT_TIMESTAMP)`, eventID, eventID)
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"event_type_slug":%q,"single_use_token":"expired","start_at":%q,"name":"Guest","email":"guest@example.test"}`, slug, time.Now().UTC().AddDate(0, 0, 10).Format(time.RFC3339))
	w := httptest.NewRecorder()
	h.CreateBooking(w, httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(body)))
	if w.Code != 410 {
		t.Fatal(w.Code, w.Body)
	}
	var count int
	if err = database.QueryRow(`SELECT count(*) FROM bookings`).Scan(&count); err != nil || count != 0 {
		t.Fatal("expired link created a booking", err)
	}
	_, err = database.Exec(`INSERT INTO event_types(id,user_id,slug,name,duration_minutes,is_active,is_public)VALUES('other',(SELECT user_id FROM event_types WHERE id=?),'other-prefill','Other',30,1,1);INSERT INTO single_use_tokens(id,token,event_type_id,created_by_user_id,created_at,prefill_json)VALUES('foreign','foreign',?,(SELECT user_id FROM event_types WHERE id=?),CURRENT_TIMESTAMP,'{"Reference":"saved"}')`, eventID, eventID, eventID)
	if err != nil {
		t.Fatal(err)
	}
	otherBody := fmt.Sprintf(`{"event_type_slug":"other-prefill","single_use_token":"foreign","start_at":%q,"name":"Guest","email":"guest@example.test"}`, time.Now().UTC().AddDate(0, 0, 10).Format(time.RFC3339))
	other := httptest.NewRecorder()
	h.CreateBooking(other, httptest.NewRequest("POST", "/v1/bookings", strings.NewReader(otherBody)))
	if other.Code != 410 {
		t.Fatal("token defaults crossed event scope", other.Code, other.Body)
	}
}
