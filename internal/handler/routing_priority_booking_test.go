package handler_test

import (
	"net/http"
	"testing"
)

func TestTeamRoutingPriorityControlsPublicAssignmentAndBusyFallback(t *testing.T) {
	h, database, key, ownerID := setupWorkspaceWithDB(t)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := database.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id,email,name,iana_timezone) VALUES('preferred','preferred@example.test','Preferred','UTC')`)
	seedFullAvailabilityDB(t, database, "preferred")
	slug, eventID := seedEventTypeHTTP(t, h, key)
	makeRoundRobin(t, h, slug, key, ownerID, "preferred")
	exec(`INSERT INTO teams(id,name,slug)VALUES('routing-team','Routing','routing-team')`)
	exec(`INSERT INTO team_members(id,team_id,user_id,routing_priority)VALUES('routing-owner','routing-team',?,10),('routing-preferred','routing-team','preferred',0)`, ownerID)
	exec(`UPDATE event_types SET team_id='routing-team',rr_strategy='priority' WHERE id=?`, eventID)

	const slot = "2027-05-15T10:00:00Z"
	for _, guest := range []string{"first", "second"} {
		if rec := postBooking(t, h, slug, slot, guest, guest+"@example.test"); rec.Code != http.StatusCreated {
			t.Fatalf("%s booking: %d %s", guest, rec.Code, rec.Body.String())
		}
	}
	for guest, wantHost := range map[string]string{"first": "preferred", "second": ownerID} {
		var host string
		if err := database.QueryRow(`SELECT b.host_id FROM bookings b JOIN booking_attendees a ON a.booking_id=b.id WHERE b.event_type_id=? AND a.email=?`, eventID, guest+"@example.test").Scan(&host); err != nil {
			t.Fatal(err)
		}
		if host != wantHost {
			t.Fatalf("%s assigned %s, want %s", guest, host, wantHost)
		}
	}
}
