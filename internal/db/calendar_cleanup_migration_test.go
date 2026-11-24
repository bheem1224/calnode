package db

import (
	"testing"

	"github.com/pressly/goose/v3"
)

func TestCalendarCleanupMigrationPreservesExistingTargetsAndRollsBack(t *testing.T) {
	database, err := Open("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	goose.SetBaseFS(migrations)
	if err = goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err = goose.UpTo(database, "migrations", 103); err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(`INSERT INTO users(id,email,name)VALUES('u','u@example.test','User');INSERT INTO event_types(id,user_id,slug,name,duration_minutes)VALUES('et','u','meeting','Meeting',30);INSERT INTO bookings(id,event_type_id,host_id,start_at,end_at,status,external_event_id,zoom_meeting_id,location_value)VALUES('bk','et','u','2099-01-01T10:00:00Z','2099-01-01T10:30:00Z','cancelled','provider-event','123','https://zoom.example.test/j/123');INSERT INTO booking_hosts(id,booking_id,user_id,is_primary,external_event_id,external_provider,external_calendar_id)VALUES('seat','bk','u',1,'provider-event','google','old-calendar');INSERT INTO calendar_event_intents(user_id,operation_key,provider,calendar_ref)VALUES('u','booking-host:seat','google','old-calendar')`)
	if err != nil {
		t.Fatal(err)
	}
	if err = Migrate(database); err != nil {
		t.Fatal(err)
	}
	var pending int
	if err = database.QueryRow(`SELECT COUNT(*) FROM calendar_event_cleanup WHERE done=0 AND event_id='provider-event' AND calendar_ref='old-calendar'`).Scan(&pending); err != nil || pending != 1 {
		t.Fatal("old cleanup target lost", pending, err)
	}
	if err = database.QueryRow(`SELECT COUNT(*) FROM zoom_meeting_intents WHERE booking_id='bk' AND user_id='u' AND meeting_id='123' AND cleanup_requested=1`).Scan(&pending); err != nil || pending != 1 {
		t.Fatal("old Zoom target lost", pending, err)
	}
	if err = goose.DownTo(database, "migrations", 103); err != nil {
		t.Fatal(err)
	}
	var original string
	if err = database.QueryRow(`SELECT zoom_meeting_id FROM bookings WHERE id='bk'`).Scan(&original); err != nil || original != "123" {
		t.Fatal("rollback changed booking", err)
	}
	if err = Migrate(database); err != nil {
		t.Fatal(err)
	}
}
