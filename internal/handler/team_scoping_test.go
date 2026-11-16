package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTeamScoping_teamHeadPermissionsAndEventTypes(t *testing.T) {
	h, database, adminKey, _ := setupWorkspaceWithDB(t)

	headKey := "team-head-key"
	memberKey := "team-member-key"
	outsiderKey := "team-outsider-key"

	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u-head','head@example.com','Team Head','UTC',0)`)
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u-mem','mem@example.com','Team Member','UTC',0)`)
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u-out','out@example.com','Outsider','UTC',0)`)

	database.Exec(`INSERT INTO api_keys (id,user_id,name,key_hash,created_at) VALUES ('k-head','u-head','t',?,'2024-01-01')`, sha256HexForTest(headKey))
	database.Exec(`INSERT INTO api_keys (id,user_id,name,key_hash,created_at) VALUES ('k-mem','u-mem','t',?,'2024-01-01')`, sha256HexForTest(memberKey))
	database.Exec(`INSERT INTO api_keys (id,user_id,name,key_hash,created_at) VALUES ('k-out','u-out','t',?,'2024-01-01')`, sha256HexForTest(outsiderKey))

	teamID, rec := createTeamHTTP(t, h, adminKey, `{"name":"Engineering"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create team: %d %s", rec.Code, rec.Body.String())
	}

	// Admin adds u-head as owner (team head) and u-mem as member.
	req := authReq(http.MethodPost, "/v1/teams/"+teamID+"/members", `{"user_id":"u-head","role":"owner"}`, adminKey)
	req.SetPathValue("id", teamID)
	rec = httptest.NewRecorder()
	h.RequireAuth(h.AddTeamMember)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("add head: %d %s", rec.Code, rec.Body.String())
	}

	req = authReq(http.MethodPost, "/v1/teams/"+teamID+"/members", `{"user_id":"u-mem","role":"member"}`, adminKey)
	req.SetPathValue("id", teamID)
	rec = httptest.NewRecorder()
	h.RequireAuth(h.AddTeamMember)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("add member: %d %s", rec.Code, rec.Body.String())
	}

	// ListTeams: head sees Engineering with my_role=owner; outsider sees 0 teams.
	rec = httptest.NewRecorder()
	h.RequireAuth(h.ListTeams)(rec, authReq(http.MethodGet, "/v1/teams", "", headKey))
	if rec.Code != http.StatusOK {
		t.Fatalf("head list teams: %d", rec.Code)
	}
	var listResp struct {
		Items []struct {
			ID     string `json:"id"`
			MyRole string `json:"my_role"`
		} `json:"items"`
	}
	json.Unmarshal(rec.Body.Bytes(), &listResp)
	if len(listResp.Items) != 1 || listResp.Items[0].ID != teamID || listResp.Items[0].MyRole != "owner" {
		t.Fatalf("unexpected head teams: %+v", listResp.Items)
	}

	rec = httptest.NewRecorder()
	h.RequireAuth(h.ListTeams)(rec, authReq(http.MethodGet, "/v1/teams", "", outsiderKey))
	json.Unmarshal(rec.Body.Bytes(), &listResp)
	if len(listResp.Items) != 0 {
		t.Fatalf("outsider should see 0 teams, got %+v", listResp.Items)
	}

	// Member can GetTeam, but cannot PatchTeam (403).
	req = authReq(http.MethodGet, "/v1/teams/"+teamID, "", memberKey)
	req.SetPathValue("id", teamID)
	rec = httptest.NewRecorder()
	h.RequireAuth(h.GetTeam)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("member get team: %d", rec.Code)
	}

	req = authReq(http.MethodPatch, "/v1/teams/"+teamID, `{"name":"Hack"}`, memberKey)
	req.SetPathValue("id", teamID)
	rec = httptest.NewRecorder()
	h.RequireAuth(h.PatchTeam)(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("member patch team: got %d; want 403", rec.Code)
	}

	// Team head can PatchTeam.
	req = authReq(http.MethodPatch, "/v1/teams/"+teamID, `{"name":"Platform Eng"}`, headKey)
	req.SetPathValue("id", teamID)
	rec = httptest.NewRecorder()
	h.RequireAuth(h.PatchTeam)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("head patch team: %d %s", rec.Code, rec.Body.String())
	}

	// Team head creates a team event type -> routing_mode defaults to collective.
	rec = httptest.NewRecorder()
	h.RequireAuth(h.CreateEventType)(rec, authReq(http.MethodPost, "/v1/event-types",
		`{"slug":"eng-sync","name":"Eng Sync","duration_minutes":30,"team_id":"`+teamID+`"}`, headKey))
	if rec.Code != http.StatusCreated {
		t.Fatalf("head create team event type: %d %s", rec.Code, rec.Body.String())
	}
	var etResp struct {
		Slug        string  `json:"slug"`
		TeamID      *string `json:"team_id"`
		TeamName    string  `json:"team_name"`
		RoutingMode string  `json:"routing_mode"`
		CanEdit     bool    `json:"can_edit"`
	}
	json.Unmarshal(rec.Body.Bytes(), &etResp)
	if etResp.TeamID == nil || *etResp.TeamID != teamID || etResp.RoutingMode != "collective" {
		t.Fatalf("unexpected team event type: %+v", etResp)
	}

	// Reject fixed routing_mode on team event type.
	rec = httptest.NewRecorder()
	h.RequireAuth(h.CreateEventType)(rec, authReq(http.MethodPost, "/v1/event-types",
		`{"slug":"eng-bad","name":"Eng Bad","duration_minutes":30,"routing_mode":"fixed","team_id":"`+teamID+`"}`, headKey))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("fixed team event type: got %d; want 400", rec.Code)
	}

	// Member sees eng-sync in ListEventTypes with can_edit=false, and gets 404 on PatchEventType.
	rec = httptest.NewRecorder()
	h.RequireAuth(h.ListEventTypes)(rec, authReq(http.MethodGet, "/v1/event-types", "", memberKey))
	if rec.Code != http.StatusOK {
		t.Fatalf("member list event types: %d", rec.Code)
	}
	var etList struct {
		Items []struct {
			Slug     string  `json:"slug"`
			TeamID   *string `json:"team_id"`
			TeamName string  `json:"team_name"`
			CanEdit  bool    `json:"can_edit"`
		} `json:"items"`
	}
	json.Unmarshal(rec.Body.Bytes(), &etList)
	if len(etList.Items) != 1 || etList.Items[0].Slug != "eng-sync" || etList.Items[0].CanEdit || etList.Items[0].TeamName != "Platform Eng" {
		t.Fatalf("unexpected member event types: %+v", etList.Items)
	}

	req = authReq(http.MethodPatch, "/v1/event-types/eng-sync", `{"name":"Member Hack"}`, memberKey)
	req.SetPathValue("slug", "eng-sync")
	rec = httptest.NewRecorder()
	h.RequireAuth(h.PatchEventType)(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("member patch team event type: got %d; want 404", rec.Code)
	}

	// Reassign team event type to personal (team_id: "") by head.
	req = authReq(http.MethodPatch, "/v1/event-types/eng-sync", `{"team_id":""}`, headKey)
	req.SetPathValue("slug", "eng-sync")
	rec = httptest.NewRecorder()
	h.RequireAuth(h.PatchEventType)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("head unassign team: %d %s", rec.Code, rec.Body.String())
	}
	json.Unmarshal(rec.Body.Bytes(), &etResp)
	if etResp.TeamID != nil {
		t.Fatalf("expected nil team_id after unassign, got %v", *etResp.TeamID)
	}
}
