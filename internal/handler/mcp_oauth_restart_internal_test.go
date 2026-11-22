package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/access"
	"github.com/calnode/calnode/internal/db"
)

func TestMCPOAuthAccessAndRefreshSurviveDatabaseReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restart.db")
	open := func() (*Handler, *sql.DB) {
		database, err := db.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Migrate(database); err != nil {
			t.Fatal(err)
		}
		h := New(database, slog.Default())
		h.SetBaseURL("https://calendar.example.test")
		return h, database
	}
	h, database := open()
	setup := httptest.NewRecorder()
	h.Setup(setup, httptest.NewRequest("POST", "/v1/setup", strings.NewReader(`{"name":"Restart Test","email":"restart@example.test","timezone":"UTC"}`)))
	if setup.Code != 201 {
		t.Fatalf("setup status=%d", setup.Code)
	}
	var user struct {
		ID string `json:"user_id"`
	}
	if err := json.Unmarshal(setup.Body.Bytes(), &user); err != nil {
		t.Fatal(err)
	}
	const accessToken, refreshToken = "mcat_restart_fixture", "mcrt_restart_fixture"
	grants := access.Grants{"mcp.connect": "own", "bookings.read": "own"}
	now := time.Now().UTC()
	if _, err := database.Exec(`INSERT INTO oauth_clients(client_id,client_name,redirect_uris,created_at) VALUES('restart-client','Restart fixture','["https://client.example.test/callback"]',?)`, now.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO oauth_access_tokens(id,token_hash,refresh_hash,client_id,user_id,scope,resource,permissions_json,expires_at,created_at) VALUES('restart-grant',?,?,'restart-client',?,'mcp',?,?,?,?)`, hashAPIKey(accessToken), hashAPIKey(refreshToken), user.ID, h.baseURL+"/mcp", grants.JSON(), now.Add(time.Hour).Format(time.RFC3339), now.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if _, err := h.VerifyMCPBearer(context.Background(), accessToken, nil); err != nil {
		t.Fatal("initial token rejected", err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	h, database = open()
	if _, err := h.VerifyMCPBearer(context.Background(), accessToken, nil); err != nil {
		t.Fatal("persisted token rejected after reopening DB", err)
	}
	// An expired access token must remain refreshable after a restart.
	if _, err := database.Exec(`UPDATE oauth_access_tokens SET expires_at=? WHERE id='restart-grant'`, now.Add(-time.Hour).Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	h, database = open()
	defer database.Close()
	if _, err := h.VerifyMCPBearer(context.Background(), accessToken, nil); err == nil {
		t.Fatal("expired access token accepted")
	}
	refresh := func(value string) (int, map[string]any) {
		form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {value}, "client_id": {"restart-client"}}
		req := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		h.TokenMCP(rec, req)
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return rec.Code, body
	}
	status, body := refresh(refreshToken)
	if status != 200 {
		t.Fatalf("persisted refresh rejected after restart: status=%d", status)
	}
	nextAccess, ok := body["access_token"].(string)
	if !ok {
		t.Fatal("no rotated access token")
	}
	principal, _, err := h.mcpPrincipal(context.Background(), nextAccess)
	if err != nil || principal.ID != user.ID || principal.Permissions["bookings.read"] != "own" || principal.Permissions.Has("settings.update") {
		t.Fatal("refreshed grant lost identity or changed scope", err)
	}
	if status, _ := refresh(refreshToken); status != 400 {
		t.Fatal("old refresh token replay accepted")
	}
	nextRefresh, ok := body["refresh_token"].(string)
	if !ok {
		t.Fatal("no rotated refresh token")
	}
	if _, err := database.Exec(`UPDATE users SET archived_at=? WHERE id=?`, now.Format(time.RFC3339), user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.VerifyMCPBearer(context.Background(), nextAccess, nil); err == nil {
		t.Fatal("archived user retained access")
	}
	if status, _ := refresh(nextRefresh); status != 400 {
		t.Fatal("archived user retained refresh access")
	}
}
