package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPasswordPolicyDefaultAndConfiguration(t *testing.T) {
	h := securitySeed(t)
	for _, test := range []struct {
		password string
		valid    bool
	}{{"short", false}, {"abcdefghijklmno", false}, {strings.Repeat("x", 16), false}, {"passwordpassword1!", false}, {"harbor meadow comet granite", true}, {strings.Repeat("界海", 32), true}, {strings.Repeat("界", 129), false}} {
		if got := h.validatePassword(context.Background(), test.password); (got == "") != test.valid {
			t.Fatalf("%q valid=%v: %s", test.password, test.valid, got)
		}
	}
	securityExec(t, h, `UPDATE server_settings SET signin_policy_json='{"password_min_length":24}' WHERE id=1`)
	if h.validatePassword(context.Background(), "distinctive phrase here") == "" {
		t.Fatal("instance minimum ignored")
	}
	status := httptest.NewRecorder()
	h.AuthStatus(status, httptest.NewRequest("GET", "/v1/auth/status", nil))
	var value map[string]any
	_ = json.Unmarshal(status.Body.Bytes(), &value)
	if value["password_min_length"] != float64(24) {
		t.Fatal("public password guidance differs from enforced policy")
	}
}

func TestDynamicAdvancedOrganizerAndValidation(t *testing.T) {
	h := securitySeed(t)
	for _, body := range []string{
		`{"host_ids":["member-b","member-a"],"organizer_id":"owner"}`,
		`{"host_ids":["member-b","member-a"],"duration_options":[15]}`,
		`{"host_ids":["member-b","member-a"],"duration_options":[30,30]}`,
		`{"host_ids":["member-b","member-a"],"location_type":"made_up"}`,
		`{"host_ids":["member-b","member-a"],"location_type":"google_meet"}`,
		`{"host_ids":["member-b","member-a"],"destination_id":"someone-elses-calendar"}`,
	} {
		rec := httptest.NewRecorder()
		h.RequireAuth(h.CreateDynamicLink)(rec, securityRequest(t, h, "member-a", "POST", "/v1/dynamic-links", body))
		if rec.Code < 400 {
			t.Fatalf("invalid override accepted: %s -> %d", body, rec.Code)
		}
	}
	for _, override := range []string{"", "member-a"} {
		body := `{"host_ids":["member-b","member-a"],"organizer_id":"` + override + `","name":"Client discussion","duration_minutes":45,"duration_options":[30,45],"location_type":"link","location_value":"https://example.test/meeting"}`
		rec := httptest.NewRecorder()
		h.RequireAuth(h.CreateDynamicLink)(rec, securityRequest(t, h, "member-a", "POST", "/v1/dynamic-links", body))
		if rec.Code != 201 {
			t.Fatalf("create: %d %s", rec.Code, rec.Body)
		}
		var response struct {
			URL       string `json:"url"`
			Organizer string `json:"organizer_id"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &response)
		want := "member-b"
		if override != "" {
			want = override
		}
		if response.Organizer != want {
			t.Fatalf("organizer=%s want %s", response.Organizer, want)
		}
		var event, owner, loc, value, options string
		var duration int
		token := response.URL[strings.LastIndex(response.URL, "/")+1:]
		if err := h.db.QueryRow(`SELECT e.id,e.user_id,e.location_type,e.location_value,e.duration_minutes,e.duration_options FROM event_types e JOIN single_use_tokens t ON t.event_type_id=e.id WHERE t.token=?`, token).Scan(&event, &owner, &loc, &value, &duration, &options); err != nil {
			t.Fatal(err)
		}
		hosts, err := h.resolveEventTypeHosts(context.Background(), event)
		if err != nil {
			t.Fatal(err)
		}
		if owner != want || hosts[0].UserID != want || loc != "link" || value != "https://example.test/meeting" || duration != 45 || options != "[30,45]" {
			t.Fatalf("settings not retained: %s %+v %s %s %d %s", owner, hosts, loc, value, duration, options)
		}
		if override == "" {
			// TestMain pins validation to June 2026; slot generation uses wall time.
			// A broad fixture range keeps this test focused on advanced settings.
			securityExec(t, h, `UPDATE event_types SET future_limit_mode='date_range',future_range_start='2000-01-01',future_range_end='2099-12-31' WHERE id=?`, event)
			for _, host := range []string{"member-a", "member-b"} {
				for day := 0; day < 7; day++ {
					securityExec(t, h, `INSERT INTO availability_rules(id,user_id,day_of_week,start_time,end_time)VALUES(?,?,?,'09:00','17:00')`, fmt.Sprintf("%s-%d", host, day), host, day)
				}
			}
			start := time.Now().UTC().Add(48 * time.Hour)
			start = time.Date(start.Year(), start.Month(), start.Day(), 9, 45, 0, 0, time.UTC)
			var testSlug string
			if err := h.db.QueryRow(`SELECT slug FROM event_types WHERE id=?`, event).Scan(&testSlug); err != nil {
				t.Fatal(err)
			}

			day := start.Format("2006-01-02")
			slotsReq := httptest.NewRequest("GET", "/v1/event-types/"+testSlug+"/slots?from="+day+"&to="+day+"&tz=UTC&token="+token+"&duration=45", nil)
			slotsReq.SetPathValue("slug", testSlug)
			offered := httptest.NewRecorder()
			h.GetSlots(offered, slotsReq)
			var availability struct {
				Slots []struct {
					Start time.Time `json:"start"`
				} `json:"slots"`
			}
			_ = json.Unmarshal(offered.Body.Bytes(), &availability)
			if offered.Code != 200 || len(availability.Slots) == 0 {
				t.Fatalf("advanced availability: %d %s", offered.Code, offered.Body)
			}
			start = availability.Slots[0].Start
			body := fmt.Sprintf(`{"event_type_slug":%q,"single_use_token":%q,"start_at":%q,"duration_minutes":45,"name":"Client","email":"client@example.test","timezone":"UTC"}`, testSlug, token, start.Format(time.RFC3339))
			booked := httptest.NewRecorder()
			h.CreateBooking(booked, httptest.NewRequest("POST", "/v1/bookings", strings.NewReader(body)))
			if booked.Code != 201 {
				t.Fatalf("advanced booking: %d %s", booked.Code, booked.Body)
			}
			var result struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(booked.Body.Bytes(), &result)
			var chosen string
			var burned bool
			if err := h.db.QueryRow(`SELECT host_id FROM bookings WHERE id=?`, result.ID).Scan(&chosen); err != nil {
				t.Fatal(err)
			}
			if err := h.db.QueryRow(`SELECT used_at IS NOT NULL FROM single_use_tokens WHERE token=?`, token).Scan(&burned); err != nil {
				t.Fatal(err)
			}
			if chosen != want || !burned {
				t.Fatalf("organizer=%s, consumed=%v", chosen, burned)
			}
		}

	}
	// Feature endpoints are classified and denied when the role lacks the grant.
	securityExec(t, h, `INSERT INTO roles(id,name,permissions_json)VALUES('no-links','No links','{"profile.read":"own"}')`)
	securityExec(t, h, `UPDATE users SET custom_role_id='no-links' WHERE id='member-a'`)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.DynamicLinkOptions)(rec, securityRequest(t, h, "member-a", "GET", "/v1/dynamic-links/options?host_ids=member-a,member-b&organizer_id=member-b", ""))
	if rec.Code != 403 {
		t.Fatalf("unscoped directory options: %d", rec.Code)
	}
}

func TestMeetingDefaultsCannotSelectUnconnectedProvider(t *testing.T) {
	h := securitySeed(t)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.PutMeetingDefaults)(rec, securityRequest(t, h, "member-a", "PUT", "/v1/users/me/meeting-defaults", `{"conferencing_type":"zoom"}`))
	if rec.Code != 400 {
		t.Fatalf("unconnected provider accepted: %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	h.RequireAuth(h.PutMeetingDefaults)(rec, securityRequest(t, h, "member-a", "PUT", "/v1/users/me/meeting-defaults", `{"conferencing_type":""}`))
	if rec.Code != 200 {
		t.Fatalf("clear default: %d %s", rec.Code, rec.Body)
	}
}
