package handler

import (
	"bytes"
	"context"
	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/calendar"
	"github.com/calnode/calnode/internal/mailer"
	"github.com/calnode/calnode/internal/zoom"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestConferenceDeliveryRetryReusesZoomMeeting(t *testing.T) {
	var logs bytes.Buffer
	h, database, _ := newReconcileFixture(t, "confirmed", &logs)
	_, err := database.Exec(`UPDATE bookings SET zoom_meeting_id='existing-meeting',location_type='zoom',location_value='https://zoom.us/j/existing' WHERE id='bk-1'`)
	if err != nil {
		t.Fatal(err)
	}
	// An existing conference remains usable even if its credentials later expire.
	_, err = database.Exec(`INSERT INTO zoom_connections(user_id,access_token_enc)VALUES('host-1','expired-credentials')`)
	if err != nil {
		t.Fatal(err)
	}
	zc, err := zoom.New(database, "test", "test", "http://localhost/callback", strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	h.SetZoom(zc)
	b := &booking.Booking{ID: "bk-1", EventTypeID: "et-1", LocationValue: "https://zoom.us/j/existing"}
	url, _, _ := h.mintMeetingLink(context.Background(), b, bookingConfirmationInput{LocationType: "zoom"}, &mailer.BookingData{}, []assignedHost{{UserID: "host-1", IsPrimary: true}})
	if url != b.LocationValue || logs.Len() != 0 {
		t.Fatalf("retry recreated an existing meeting: %s %s", url, logs.String())
	}
}

type meetingCalendarRecorder struct {
	stuckProvider
	mu     sync.Mutex
	events map[string]calendar.CreateEventParams
}

func (p *meetingCalendarRecorder) Name() string { return "google" }
func (p *meetingCalendarRecorder) HasDestination(context.Context, string) (bool, error) {
	return true, nil
}
func (p *meetingCalendarRecorder) ListCalendars(context.Context, string, string) ([]calendar.CalendarInfo, error) {
	return []calendar.CalendarInfo{{ID: "selected", Name: "Private calendar name", Writable: true}}, nil
}
func (p *meetingCalendarRecorder) CreateEvent(_ context.Context, id string, in calendar.CreateEventParams) (string, string, string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events[id] = in
	return "event-" + id, "", "selected", nil
}

func TestMeetingDeliveryCalendarOverrideAndPrivacy(t *testing.T) {
	var logs bytes.Buffer
	h, db, _ := newReconcileFixture(t, "confirmed", &logs)
	for _, q := range []string{
		`INSERT INTO users(id,email,name)VALUES('host-2','other@example.test','Other')`,
		`INSERT INTO booking_hosts(id,booking_id,user_id,is_primary)VALUES('bh-2','bk-1','host-2',0)`,
		`INSERT INTO booking_attendees(id,booking_id,name,email,is_organizer,iana_timezone)VALUES('client','bk-1','Client','client@example.test',1,'UTC')`,
		`UPDATE event_types SET destination_calendar_id='selected',hide_notes_in_calendar=1,hide_calendar_event_details=1 WHERE id='et-1'`,
		`INSERT INTO calendar_connections(id,user_id,provider,account_email,calendar_id,access_token_enc,is_destination)VALUES('c1','host-1','google','private@example.test','primary','test',1),('c2','host-2','google','other@example.test','primary','test',1)`,
		`INSERT INTO connection_calendars(id,user_id,provider,account_email,calendar_id,name)VALUES('choice','host-1','google','private@example.test','selected','Private calendar name')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	p := &meetingCalendarRecorder{events: map[string]calendar.CreateEventParams{}}
	svc := calendar.NewService(db)
	svc.Register(p)
	h.SetCalendar(svc)
	start := time.Now().UTC().Add(time.Hour)
	b := &booking.Booking{ID: "bk-1", EventTypeID: "et-1", StartAt: start, EndAt: start.Add(45 * time.Minute), Notes: "Do not send these private notes", InviteDelivery: "host"}
	hosts := []assignedHost{{UserID: "host-1", IsPrimary: true}, {UserID: "host-2"}}
	h.createHostEvents(context.Background(), b, bookingConfirmationInput{EventTypeName: "Client review", OrganizerEmail: "client@example.test", OrganizerName: "Client"}, &mailer.BookingData{}, hosts, "https://meet.google.com/abc-defg-hij", false, "")
	if len(p.events) != 2 {
		t.Fatalf("calendar deliveries=%d: %s", len(p.events), logs.String())
	}
	if p.events["host-1"].DestinationCalendarID != "selected" || p.events["host-2"].DestinationCalendarID != "" {
		t.Fatal("primary override leaked to co-host's calendar")
	}
	for host, event := range p.events {
		if (host == "host-1" && event.OrganizerEmail != "client@example.test") || (host == "host-2" && event.OrganizerEmail != "") {
			t.Fatal("the primary organizer should send the only client invitation")
		}
		if !event.Private || event.End.Sub(event.Start) != 45*time.Minute || strings.Contains(event.Description, "Do not send these private notes") {
			t.Fatalf("delivery settings lost: %+v", event)
		}
	}

	if _, err := db.Exec(`UPDATE booking_hosts SET external_event_id=NULL WHERE booking_id='bk-1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE bookings SET created_at=?,guests_json='["guest@example.test"]' WHERE id='bk-1'`, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}

	p.events = map[string]calendar.CreateEventParams{}
	if _, err := db.Exec(`UPDATE bookings SET pending_approval=1 WHERE id='bk-1'`); err != nil {
		t.Fatal(err)
	}
	h.reconcileCreations(context.Background(), svc)
	if len(p.events) != 0 {
		t.Fatal("repair bypassed host approval")
	}
	if _, err := db.Exec(`UPDATE bookings SET pending_approval=0,payment_status='pending' WHERE id='bk-1'`); err != nil {
		t.Fatal(err)
	}
	h.reconcileCreations(context.Background(), svc)
	if len(p.events) != 0 {
		t.Fatal("repair bypassed payment")
	}
	if _, err := db.Exec(`UPDATE bookings SET payment_status='paid' WHERE id='bk-1'`); err != nil {
		t.Fatal(err)
	}
	h.reconcileCreations(context.Background(), svc)
	if !p.events["host-1"].Private || p.events["host-1"].DestinationCalendarID != "selected" || len(p.events["host-1"].Guests) != 1 || len(p.events["host-2"].Guests) != 0 {
		t.Fatalf("retry lost settings: %+v", p.events)
	}
	foreign, err := h.meetingCalendars(context.Background(), "host-2", "host-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(foreign) != 1 || foreign[0].ID != "choice" || foreign[0].Label == "Private calendar name" {
		t.Fatalf("private calendar metadata disclosed: %+v", foreign)
	}
	own, err := h.meetingCalendars(context.Background(), "host-1", "host-1")
	if err != nil || own[0].Label != "Private calendar name" {
		t.Fatal("own calendar names unavailable")
	}
}
