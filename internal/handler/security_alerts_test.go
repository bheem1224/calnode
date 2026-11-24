package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/calnode/calnode/internal/mailer"
)

type alertTestMailer struct{ messages []mailer.Message }

func (m *alertTestMailer) Send(_ context.Context, msg mailer.Message) error {
	m.messages = append(m.messages, msg)
	return nil
}
func alertSeed(t *testing.T) (*Handler, *alertTestMailer) {
	t.Helper()
	h := securitySeed(t)
	securityExec(t, h, `INSERT INTO users(id,email,name,is_admin,is_owner)VALUES('owner','owner@example.com','Owner',1,1),('it','it@example.com','IT',1,0),('other','other@example.com','Other admin',1,0)`)
	m := &alertTestMailer{}
	h.SetMailer(m, "https://scheduler.example.test")
	return h, m
}
func TestAdministratorAlertRecipients(t *testing.T) {
	for _, tc := range []struct {
		name, mode string
		ids        []string
		change     string
		want       []string
	}{
		{"default owner", "", nil, "", []string{"a@example.com", "owner@example.com"}},
		{"all admins", "admins", nil, "", []string{"a@example.com", "it@example.com", "other@example.com", "owner@example.com"}},
		{"selected only", "selected", []string{"it"}, "", []string{"a@example.com", "it@example.com"}},
		{"archived recipient", "selected", []string{"it"}, `UPDATE users SET archived_at=datetime('now') WHERE id='it'`, []string{"a@example.com", "owner@example.com"}},
		{"demoted recipient", "selected", []string{"it"}, `UPDATE users SET is_admin=0 WHERE id='it'`, []string{"a@example.com", "owner@example.com"}},
		{"owner transfer", "owner", nil, `UPDATE users SET is_owner=(id='other')`, []string{"a@example.com", "other@example.com"}},
		{"ownerless migration", "owner", nil, `UPDATE users SET is_owner=0`, []string{"a@example.com", "it@example.com", "other@example.com", "owner@example.com"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, m := alertSeed(t)
			p, _ := h.signinPolicy(context.Background())
			p.AdminAlertMode = tc.mode
			p.AdminAlertUserIDs = tc.ids
			setTestSigninPolicy(t, h, p)
			if tc.change != "" {
				securityExec(t, h, tc.change)
			}
			if err := h.JobSecurityNotify(context.Background(), `{"user_id":"member-a","action":"MFA reset"}`); err != nil {
				t.Fatal(err)
			}
			got := []string{}
			for _, msg := range m.messages {
				got = append(got, msg.To...)
				if msg.Subject != "Calnode security notification" {
					t.Fatal(msg.Subject)
				}
			}
			sort.Strings(got)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("recipients=%v want %v", got, tc.want)
			}
		})
	}
}
func TestAdministratorAlertPolicyAPI(t *testing.T) {
	h, _ := alertSeed(t)
	cookie := securitySession(t, h, "owner", true, false)
	p, _ := h.signinPolicy(context.Background())
	p.AdminAlertMode = "selected"
	p.AdminAlertUserIDs = []string{"it", "it"}
	put := func(p SigninPolicy) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(p)
		return securityCall(h, h.PutSigninPolicy, cookie, "PUT", "/v1/settings/security", string(raw), nil)
	}
	if rec := put(p); rec.Code != 200 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	saved, _ := h.signinPolicy(context.Background())
	if !reflect.DeepEqual(saved.AdminAlertUserIDs, []string{"it"}) {
		t.Fatal(saved.AdminAlertUserIDs)
	}
	for _, bad := range []SigninPolicy{{AdminAlertMode: "unknown"}, {AdminAlertMode: "selected"}, {AdminAlertMode: "selected", AdminAlertUserIDs: []string{"member-a"}}, {AdminAlertMode: "selected", AdminAlertUserIDs: []string{"missing"}}} {
		if rec := put(bad); rec.Code != 400 {
			t.Fatalf("invalid config accepted: %d %s", rec.Code, rec.Body)
		}
	}
	member := securitySession(t, h, "member-a", true, false)
	if rec := securityCall(h, h.PutSigninPolicy, member, "PUT", "/v1/settings/security", `{"admin_alert_mode":"admins"}`, nil); rec.Code != 403 {
		t.Fatalf("member changed recipients: %d", rec.Code)
	}
	securityExec(t, h, `UPDATE sessions SET reauthenticated_at=NULL WHERE user_id='owner'`)
	if rec := put(p); rec.Code != 403 {
		t.Fatalf("unverified recipient change: %d", rec.Code)
	}
}
func TestCalendarAlertCooldownAndContent(t *testing.T) {
	h, m := alertSeed(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		h.recordCalendarHealth(ctx, "member-a", errors.New("provider unavailable"))
		h.recordCalendarHealth(ctx, "member-a", nil)
	}
	var count int
	var payload string
	if err := h.db.QueryRow(`SELECT COUNT(*),payload FROM jobs WHERE type='security.notify'`).Scan(&count, &payload); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("repeated recovery generated %d alerts", count)
	}
	if err := h.JobSecurityNotify(ctx, payload); err != nil {
		t.Fatal(err)
	}
	for _, msg := range m.messages {
		if msg.Subject != "Calnode calendar connection needs attention" || !strings.Contains(msg.Text, "https://scheduler.example.test/admin/calendar") || strings.Contains(msg.Text, "Members & Roles") || strings.Contains(msg.Text, "Reference:") {
			t.Fatalf("incorrect calendar message: %+v", msg)
		}
	}
	securityExec(t, h, `UPDATE jobs SET status='done',finished_at=datetime('now'),created_at=datetime('now','-7 hours') WHERE type='security.notify'`)
	h.recordCalendarHealth(ctx, "member-a", errors.New("provider unavailable"))
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE type='security.notify'`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("later failure not notified: %d %v", count, err)
	}
}
func TestCancelledAvailabilityRequestsDoNotChangeCalendarHealth(t *testing.T) {
	h, _ := alertSeed(t)
	h.recordCalendarHealth(context.Background(), "member-a", context.Canceled)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h.recordCalendarHealth(ctx, "member-a", errors.New("wrapped cancellation"))
	var count int
	var health string
	_ = h.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE type='security.notify'`).Scan(&count)
	_ = h.db.QueryRow(`SELECT calendar_check_error FROM users WHERE id='member-a'`).Scan(&health)
	if count != 0 || health != "" {
		t.Fatalf("cancelled request marked unhealthy: count=%d health=%q", count, health)
	}
}
