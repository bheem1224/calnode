package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSingleUseSecondsExpiryIsAuthoritativeAndValidated(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)
	_, eventID := seedEventTypeHTTP(t, h, key)
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"thirty seconds", `{"expires_in_seconds":30}`, 201},
		{"zero", `{"expires_in_seconds":0}`, 400},
		{"negative", `{"expires_in_seconds":-1}`, 400},
		{"over maximum", `{"expires_in_seconds":604801}`, 400},
		{"overflow", `{"expires_in_seconds":9223372036854775808}`, 400},
		{"fraction", `{"expires_in_seconds":0.5}`, 400},
		{"conflicting units", `{"expires_in_seconds":30,"expires_in_hours":72}`, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := time.Now()
			req := authReq("POST", "/v1/event-types/"+eventID+"/single-use-links", tc.body, key)
			req.SetPathValue("id", eventID)
			rec := httptest.NewRecorder()
			h.RequireAuth(h.CreateSingleUseLink)(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status=%d want %d: %s", rec.Code, tc.status, rec.Body.String())
			}
			if tc.status != 201 {
				return
			}
			var link struct {
				ID, Token string
				ExpiresAt string `json:"expires_at"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &link); err != nil {
				t.Fatal(err)
			}
			expires, err := time.Parse(time.RFC3339Nano, link.ExpiresAt)
			if err != nil || expires.Before(before.Add(30*time.Second)) || expires.After(time.Now().Add(30*time.Second)) {
				t.Fatalf("expiry did not match requested duration: %v", err)
			}
			var stored string
			if err := database.QueryRow(`SELECT expires_at FROM single_use_tokens WHERE id=?`, link.ID).Scan(&stored); err != nil {
				t.Fatal(err)
			}
			if stored != link.ExpiresAt {
				t.Fatal("response expiry differs from authoritative token expiry")
			}
			// Exercise real public token resolution after its authoritative expiry.
			if _, err := database.Exec(`UPDATE single_use_tokens SET expires_at=? WHERE id=?`, before.Add(-time.Second).UTC().Format(time.RFC3339Nano), link.ID); err != nil {
				t.Fatal(err)
			}
			page := httptest.NewRecorder()
			visit := httptest.NewRequest(http.MethodGet, "/t/"+link.Token, nil)
			visit.SetPathValue("token", link.Token)
			h.SingleUseBookPage(page, visit)
			if page.Code != http.StatusGone {
				t.Fatalf("expired public link status=%d", page.Code)
			}
		})
	}
	var count int
	if err := database.QueryRow(`SELECT count(*) FROM single_use_tokens WHERE event_type_id=?`, eventID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("invalid requests minted tokens: count=%d error=%v", count, err)
	}
}
