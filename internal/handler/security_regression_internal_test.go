package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/access"
	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/calendar"
	"github.com/calnode/calnode/internal/slots"
)

func securityExec(t *testing.T, h *Handler, q string, args ...any) {
	t.Helper()
	if _, err := h.db.Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}

func TestImpersonationCannotEscalateOrOutliveAuthority(t *testing.T) {
	h := securitySeed(t)
	securityExec(t, h, `INSERT INTO roles(id,name,permissions_json)VALUES('support','Support','{"impersonation.use":"all","profile.read":"own","profile.update":"own"}')`)
	securityExec(t, h, `UPDATE users SET custom_role_id='support' WHERE id='member-a'`)
	securityExec(t, h, `UPDATE users SET is_admin=1 WHERE id='member-b'`)
	cookie := securitySession(t, h, "member-a", false, false)
	rec := securityCall(h, h.StartImpersonation, cookie, "POST", "/", `{"user_id":"member-b"}`, nil)
	if rec.Code != 403 {
		t.Fatalf("delegated support impersonated Admin: %d %s", rec.Code, rec.Body)
	}
	securityExec(t, h, `UPDATE users SET is_admin=0 WHERE id='member-b'`)
	rec = securityCall(h, h.StartImpersonation, cookie, "POST", "/", `{"user_id":"member-b"}`, nil)
	if rec.Code != 200 {
		t.Fatalf("Member support: %d %s", rec.Code, rec.Body)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(cookie)
	if _, _, ok := h.sessionUser(req); ok {
		t.Fatal("support session could authorize MCP")
	}
	if got := securityCall(h, h.CreateAPIKey, cookie, "POST", "/", "{}", nil); got.Code != 403 {
		t.Fatalf("support session could create a persistent key: %d", got.Code)
	}
	if got := securityCall(h, h.GetMe, cookie, "GET", "/", "", nil); got.Code != 200 {
		t.Fatalf("valid support session: %d %s", got.Code, got.Body)
	}
	securityExec(t, h, `UPDATE roles SET permissions_json='{}' WHERE id='support'`)
	if got := securityCall(h, h.GetMe, cookie, "GET", "/", "", nil); got.Code != 401 {
		t.Fatal("support session survived permission revocation")
	}
}

func TestDynamicLinksScopeExpiryAndLiveCalendarFailure(t *testing.T) {
	h := securitySeed(t)
	securityExec(t, h, `INSERT INTO users(id,email,name)VALUES('member-c','c@example.com','C')`)
	if !access.Member(false).Has("dynamic_links.create") || access.Member(false)["dynamic_links.create"] != "all" {
		t.Fatal("Member default must permit instance-wide one-time scheduling")
	}
	securityExec(t, h, `INSERT INTO teams(id,name,slug)VALUES('team','Team','team')`)
	securityExec(t, h, `INSERT INTO team_members(id,team_id,user_id)VALUES('a','team','member-a'),('b','team','member-b')`)
	for _, test := range []struct {
		scope string
		hosts []string
		want  bool
	}{{"team", []string{"member-a", "member-b"}, true}, {"team", []string{"member-a", "member-c"}, false}, {"own", []string{"member-a", "member-b"}, false}, {"all", []string{"member-b", "member-c"}, true}} {
		if got := access.DynamicHostsAllowed(context.Background(), h.db, "member-a", test.scope, test.hosts); got != test.want {
			t.Fatalf("scope %s hosts %v=%v", test.scope, test.hosts, got)
		}
	}
	req := securityRequest(t, h, "member-a", "POST", "/v1/dynamic-links", `{"host_ids":["member-a","member-b"]}`)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateDynamicLink)(rec, req)
	if rec.Code != 201 {
		t.Fatalf("mint: %d %s", rec.Code, rec.Body)
	}
	var link struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &link); err != nil {
		t.Fatal(err)
	}
	token := link.URL[strings.LastIndex(link.URL, "/")+1:]
	var slug string
	if err := h.db.QueryRow(`SELECT e.slug FROM single_use_tokens s JOIN event_types e ON e.id=s.event_type_id WHERE s.token=?`, token).Scan(&slug); err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().AddDate(0, 0, 2)
	start = time.Date(start.Year(), start.Month(), start.Day(), 10, 0, 0, 0, time.UTC)
	for _, id := range []string{"member-a", "member-b"} {
		for day := 0; day < 7; day++ {
			securityExec(t, h, `INSERT INTO availability_rules(id,user_id,day_of_week,start_time,end_time)VALUES(?,?,?,'09:00','17:00')`, fmt.Sprintf("%s-%d", id, day), id, day)
		}
	}
	cal := calendar.NewService(h.db)
	cal.Register(&securityFailedCalendar{})
	h.SetCalendar(cal)
	body := fmt.Sprintf(`{"event_type_slug":%q,"single_use_token":%q,"start_at":%q,"duration_minutes":30,"name":"Client","email":"client@example.com","timezone":"UTC"}`, slug, token, start.Format(time.RFC3339))
	rec = httptest.NewRecorder()
	h.CreateBooking(rec, httptest.NewRequest("POST", "/v1/bookings", strings.NewReader(body)))
	if rec.Code == 201 || rec.Code < 400 {
		t.Fatalf("calendar failure accepted: %d %s", rec.Code, rec.Body)
	}
	var burned bool
	var count int
	if err := h.db.QueryRow(`SELECT used_at IS NOT NULL FROM single_use_tokens WHERE token=?`, token).Scan(&burned); err != nil {
		t.Fatal(err)
	}
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM bookings`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if burned || count != 0 {
		t.Fatal("calendar failure burned the token or persisted a booking")
	}
	securityExec(t, h, `INSERT INTO roles(id,name,permissions_json)VALUES('restricted','Restricted','{}')`)
	securityExec(t, h, `UPDATE users SET custom_role_id='restricted' WHERE id='member-a'`)
	if h.dynamicLinkAllowed(context.Background(), token) {
		t.Fatal("revoked creator permission retained dynamic authority")
	}
	// The generic booking endpoint must recheck the same authority as /t/{token}.
	// Knowing the internal slug and an otherwise unexpired token is not a bypass.
	rec = httptest.NewRecorder()
	h.CreateBooking(rec, httptest.NewRequest("POST", "/v1/bookings", strings.NewReader(body)))
	if rec.Code != http.StatusGone {
		t.Fatalf("revoked dynamic token on generic booking route: %d %s", rec.Code, rec.Body)
	}
	securityExec(t, h, `UPDATE users SET custom_role_id=NULL WHERE id='member-a'`)
	securityExec(t, h, `UPDATE single_use_tokens SET expires_at='2000-01-01T00:00:00Z' WHERE token=?`, token)
	req = httptest.NewRequest("GET", "/t/"+token, nil)
	req.SetPathValue("token", token)
	rec = httptest.NewRecorder()
	h.SingleUseBookPage(rec, req)
	if rec.Code != 410 {
		t.Fatalf("expired link: %d", rec.Code)
	}
}
func securitySeed(t *testing.T) *Handler {
	h := newInternalHandler(t)
	securityExec(t, h, `INSERT INTO users(id,email,name,iana_timezone)VALUES('member-a','a@example.com','Member A','UTC'),('member-b','b@example.com','Member B','UTC')`)
	securityExec(t, h, `INSERT INTO event_types(id,user_id,slug,name,duration_minutes,slot_interval_minutes,location_type)VALUES('event-a','member-a','audit-event','Audit',30,30,'link')`)
	return h
}
func securityRequest(t *testing.T, h *Handler, user, method, path, body string) *http.Request {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := h.createSession(context.Background(), rec, user); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.AddCookie(rec.Result().Cookies()[0])
	return req
}
func TestSecurityRegressionDelegatedPasswordEscalation(t *testing.T) {
	h := securitySeed(t)
	securityExec(t, h, `INSERT INTO roles(id,name,permissions_json)VALUES('helpdesk','Helpdesk','{"members.password":"all"}')`)
	securityExec(t, h, `UPDATE users SET custom_role_id='helpdesk' WHERE id='member-a'`)
	securityExec(t, h, `UPDATE users SET is_admin=1 WHERE id='member-b'`)
	req := securityRequest(t, h, "member-a", "POST", "/v1/users/member-b/password", `{"password":"Audit-only-password-123"}`)
	req.SetPathValue("id", "member-b")
	rec := httptest.NewRecorder()
	h.RequireAuth(h.AdminSetPassword)(rec, req)
	if rec.Code != 403 {
		t.Fatalf("security boundary failed: %d %s", rec.Code, rec.Body.String())
	}

}
func TestSecurityRegressionCrossTeamHostAssignment(t *testing.T) {
	h := securitySeed(t)
	req := securityRequest(t, h, "member-a", "PUT", "/v1/event-types/audit-event/hosts", `{"hosts":[{"user_id":"member-b","role":"required"}]}`)
	req.SetPathValue("slug", "audit-event")
	rec := httptest.NewRecorder()
	h.RequireAuth(h.SetEventTypeHosts)(rec, req)
	if rec.Code != 403 {
		t.Fatalf("security boundary failed: %d %s", rec.Code, rec.Body.String())
	}

}
func TestSecurityRegressionTeamHeadCanRecruitAndPromote(t *testing.T) {
	h := securitySeed(t)
	securityExec(t, h, `INSERT INTO teams(id,name,slug)VALUES('team-a','Team A','a'),('team-b','Team B','b')`)
	securityExec(t, h, `INSERT INTO team_members(id,team_id,user_id,role)VALUES('ma','team-a','member-a','owner'),('mb','team-b','member-b','member')`)
	req := securityRequest(t, h, "member-a", "POST", "/v1/teams/team-a/members", `{"user_id":"member-b","role":"owner"}`)
	req.SetPathValue("id", "team-a")
	rec := httptest.NewRecorder()
	h.RequireAuth(h.AddTeamMember)(rec, req)
	if rec.Code != 403 {
		t.Fatalf("security boundary failed: %d %s", rec.Code, rec.Body.String())
	}

}

type securityFailedCalendar struct{ calendar.Provider }

func (p *securityFailedCalendar) Name() string { return "audit" }
func (p *securityFailedCalendar) FreeBusy(context.Context, string, time.Time, time.Time) ([]slots.Interval, error) {
	return nil, errors.New("audit simulated revoked calendar")
}
func TestSecurityRegressionAnonymousGroupIgnoresCalendarFailure(t *testing.T) {
	h := securitySeed(t)
	start := time.Now().UTC().AddDate(0, 0, 2)
	start = time.Date(start.Year(), start.Month(), start.Day(), 10, 0, 0, 0, time.UTC)
	for _, id := range []string{"member-a", "member-b"} {
		for day := 0; day < 7; day++ {
			securityExec(t, h, `INSERT INTO availability_rules(id,user_id,day_of_week,start_time,end_time)VALUES(?,?,?,'09:00','17:00')`, fmt.Sprintf("%s-%d", id, day), id, day)
		}
	}
	cal := calendar.NewService(h.db)
	cal.Register(&securityFailedCalendar{})
	h.SetCalendar(cal)
	req := httptest.NewRequest("POST", "/v1/public/group/id:member-a+id:member-b/bookings", strings.NewReader(fmt.Sprintf(`{"start_at":%q,"duration_minutes":30,"attendee_name":"Test","attendee_email":"test@example.com"}`, start.Format(time.RFC3339))))
	req.SetPathValue("handles", "id:member-a+id:member-b")
	rec := httptest.NewRecorder()
	h.CreateDynamicGroupBooking(rec, req)
	if rec.Code != 410 {
		t.Fatalf("security boundary failed: %d %s", rec.Code, rec.Body.String())
	}
	var jobs int
	_ = h.db.QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&jobs)
	t.Logf("Unsigned group path rejected; scheduled jobs=%d", jobs)
}
func TestSecurityRegressionPublicBookingDisclosesPrivateJoinLink(t *testing.T) {
	h := securitySeed(t)
	securityExec(t, h, `INSERT INTO bookings(id,event_type_id,host_id,start_at,end_at,status,location_value,cancellation_reason)VALUES('known-booking-id','event-a','member-a','2026-12-01T10:00:00Z','2026-12-01T10:30:00Z','confirmed','https://meeting.example/private-secret','Confidential internal reason')`)
	req := httptest.NewRequest("GET", "/v1/bookings/known-booking-id", nil)
	req.SetPathValue("id", "known-booking-id")
	rec := httptest.NewRecorder()
	h.GetBooking(rec, req)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "private-secret") || strings.Contains(rec.Body.String(), "Confidential internal reason") {
		t.Fatalf("security boundary failed: %d %s", rec.Code, rec.Body.String())
	}

}
func TestSecurityRegressionApprovalOverlappingExistingBooking(t *testing.T) {
	h := securitySeed(t)
	securityExec(t, h, `UPDATE event_types SET unconfirmed_blocks_slots=0 WHERE id='event-a'`)
	securityExec(t, h, `INSERT INTO bookings(id,event_type_id,host_id,start_at,end_at,status,pending_approval)VALUES('pending','event-a','member-a','2026-12-01T10:00:00Z','2026-12-01T11:00:00Z','confirmed',1),('confirmed','event-a','member-a','2026-12-01T10:30:00Z','2026-12-01T11:30:00Z','confirmed',0)`)
	securityExec(t, h, `INSERT INTO booking_hosts(id,booking_id,user_id,is_primary)VALUES('bh1','pending','member-a',1),('bh2','confirmed','member-a',1)`)
	b, err := h.bookingSvc.Approve(context.Background(), "pending")
	if !errors.Is(err, booking.ErrDoubleBooked) {
		t.Fatalf("security boundary failed: booking=%v err=%v", b, err)
	}

}

func TestSecurityRegressionMemberCanBlockWholeTeam(t *testing.T) {
	h := securitySeed(t)
	securityExec(t, h, `INSERT INTO teams(id,name,slug)VALUES('team-a','Team A','team-a')`)
	securityExec(t, h, `INSERT INTO team_members(id,team_id,user_id,role)VALUES('tma','team-a','member-a','member'),('tmb','team-a','member-b','member')`)
	date := time.Now().UTC().AddDate(0, 0, 2).Format("2006-01-02")
	req := securityRequest(t, h, "member-a", "POST", "/v1/ooo", fmt.Sprintf(`{"team_id":"team-a","dates":[%q],"reason":"ooo"}`, date))
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateOOOEntry)(rec, req)
	if rec.Code != 403 {
		t.Fatalf("security boundary failed: %d %s", rec.Code, rec.Body.String())
	}
	dates, err := h.loadUserOOODates(context.Background(), "member-b")
	if err != nil || len(dates) != 0 {
		t.Fatalf("other team member not affected: %v %v", dates, err)
	}

}
