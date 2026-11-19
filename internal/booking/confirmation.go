package booking

import (
	"strings"
	"time"
)

// confirmationRequired is evaluated at the transactional write boundary, whichever
// transport created the booking. UI and agent callers cannot bypass host approval.
func confirmationRequired(required bool, mode string, notice int, email string, start, now time.Time) bool {
	if !required {
		return false
	}
	switch mode {
	case "under_notice":
		return start.Sub(now) < time.Duration(notice)*time.Minute
	case "free_email":
		_, domain, ok := strings.Cut(strings.ToLower(strings.TrimSpace(email)), "@")
		if !ok {
			return false
		}
		switch domain {
		case "gmail.com", "googlemail.com", "outlook.com", "hotmail.com", "live.com", "msn.com", "yahoo.com", "icloud.com", "me.com", "aol.com", "proton.me", "protonmail.com":
			return true
		}
		return false
	default:
		return true
	}
}

// RescheduledEnd preserves the booked interval, including non-default and ad-hoc
// durations. The event default is only a fallback for a legacy invalid interval.
func RescheduledEnd(oldStart, oldEnd, newStart time.Time, fallbackMinutes int) time.Time {
	length := oldEnd.Sub(oldStart)
	if length <= 0 {
		length = time.Duration(fallbackMinutes) * time.Minute
	}
	return newStart.Add(length)
}
