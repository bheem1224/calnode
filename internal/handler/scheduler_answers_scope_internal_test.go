package handler

import (
	"net/http/httptest"
	"testing"

	"github.com/calnode/calnode/internal/access"
)

func TestSchedulerAnswerReadUsesSharedTeamScope(t *testing.T) {
	h := rbacFixture(t)
	rbacActor(t, h, "scheduler", access.Grants{"bookings.read": "team", "bookings.answers": "team"}, "a", "member")
	var host string
	if err := h.db.QueryRow(`SELECT user_id FROM event_types WHERE id='team-a'`).Scan(&host); err != nil {
		t.Fatal(err)
	}
	for i, team := range []string{"a", "b"} {
		rbacExec(t, h, `INSERT INTO bookings(id,event_type_id,host_id,start_at,end_at)VALUES(?,?,?,?,?)`, "answer-"+team, "team-"+team, host, []string{"2027-02-01T10:00:00Z", "2027-02-02T10:00:00Z"}[i], []string{"2027-02-01T10:30:00Z", "2027-02-02T10:30:00Z"}[i])
	}
	rbacExec(t, h, `INSERT INTO event_type_questions(id,event_type_id,label,type,position)VALUES('room-question','team-a','Data room URL','url',0)`)
	rbacExec(t, h, `INSERT INTO booking_answers(booking_id,question_id,value)VALUES('answer-a','room-question','"https://room.example.test"')`)
	for _, channel := range []string{"session", "key", "mcp"} {
		for _, team := range []string{"a", "b"} {
			r := httptest.NewRequest("GET", "/v1/bookings/answer-"+team+"/answers", nil)
			r.SetPathValue("id", "answer-"+team)
			r.Header.Set("X-API-Key", "scheduler-key-secret")
			w := httptest.NewRecorder()
			rbacEndpoint(t, h, channel, "scheduler", h.GetBookingAnswers, w, r)
			want := 200
			if team == "b" {
				want = 404
			}
			if w.Code != want {
				t.Fatalf("%s team %s: got %d want %d (%s)", channel, team, w.Code, want, w.Body.String())
			}
		}
	}
}
