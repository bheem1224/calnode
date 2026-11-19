package booking

import (
	"testing"
	"time"
)

func TestConfirmationPolicy(t *testing.T) {
	now := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, mode, email string
		required          bool
		lead              time.Duration
		want              bool
	}{
		{"off", "always", "a@gmail.com", false, time.Hour, false},
		{"always", "always", "a@company.test", true, 24 * time.Hour, true},
		{"short notice", "under_notice", "a@company.test", true, 30 * time.Minute, true},
		{"outside notice", "under_notice", "a@company.test", true, 2 * time.Hour, false},
		{"free email", "free_email", "A@GMAIL.COM", true, time.Hour, true},
		{"corporate email", "free_email", "a@company.test", true, time.Hour, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := confirmationRequired(tc.required, tc.mode, 60, tc.email, now.Add(tc.lead), now); got != tc.want {
				t.Fatalf("got %v; want %v", got, tc.want)
			}
		})
	}
}
func TestRescheduledEndPreservesBookedDuration(t *testing.T) {
	start := time.Now().UTC()
	next := start.Add(24 * time.Hour)
	for _, duration := range []time.Duration{15 * time.Minute, 45 * time.Minute, 73 * time.Minute} {
		if got := RescheduledEnd(start, start.Add(duration), next, 30).Sub(next); got != duration {
			t.Fatalf("got %v; want %v", got, duration)
		}
	}
	if got := RescheduledEnd(start, start, next, 30).Sub(next); got != 30*time.Minute {
		t.Fatal(got)
	}
}
