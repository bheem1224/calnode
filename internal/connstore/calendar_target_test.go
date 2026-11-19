package connstore

import (
	"context"
	"strings"
	"testing"
)

func TestCalendarReferenceSurvivesSelectionChanges(t *testing.T) {
	database := newDestDB(t)
	ref := CalendarReference("google", "primary", "second@example.test")
	provider, id, account, err := CalendarTarget(context.Background(), database, "host", ref)
	if err != nil || provider != "google" || id != "primary" || account != "second@example.test" {
		t.Fatalf("target: %s %s %s %v", provider, id, account, err)
	}
	// References do not depend on volatile OAuth connection or selection row IDs.
	if _, err = database.Exec(`DELETE FROM connection_calendars`); err != nil {
		t.Fatal(err)
	}
	_, _, account, err = CalendarTarget(context.Background(), database, "host", ref)
	if err != nil || account != "second@example.test" {
		t.Fatal("reference changed when selections were saved")
	}
	for _, bad := range []string{CalendarReferencePrefix + "invalid", CalendarReference("evil", "primary", ""), CalendarReferencePrefix + strings.Repeat("A", 9000)} {
		if _, _, _, err = CalendarTarget(context.Background(), database, "host", bad); err == nil {
			t.Fatal("malformed reference accepted")
		}
	}
}
