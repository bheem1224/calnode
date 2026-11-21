package worker

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const webhookRetryWindow = 72 * time.Hour

type deliveryFailure struct {
	status int
	after  time.Time
}

func (e *deliveryFailure) Error() string {
	return fmt.Sprintf("webhook endpoint returned HTTP %d", e.status)
}
func webhookJob(typ string) bool { return typ == "webhook.deliver" || typ == "webhook.enqueue" }
func jobCreated(raw string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05"} {
		if t, e := time.Parse(layout, raw); e == nil {
			return t
		}
	}
	return time.Time{}
}
func webhookExpired(created string, now time.Time) bool {
	t := jobCreated(created)
	return t.IsZero() || !now.Before(t.Add(webhookRetryWindow))
}
func retryAt(typ, created string, attempt int, err error, now time.Time) (time.Time, bool) {
	if !webhookJob(typ) {
		return now.Add(backoff(attempt)), false
	}
	if webhookExpired(created, now) {
		return time.Time{}, true
	}
	var failure *deliveryFailure
	if errors.As(err, &failure) && failure.status >= 400 && failure.status < 500 && failure.status != 408 && failure.status != 425 && failure.status != 429 {
		return time.Time{}, true
	}
	delays := []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, 30 * time.Minute, time.Hour, 2 * time.Hour, 4 * time.Hour, 6 * time.Hour}
	index := attempt - 1
	if index < 0 {
		index = 0
	}
	if index >= len(delays) {
		index = len(delays) - 1
	}
	delay := time.Duration(float64(delays[index]) * (0.9 + rand.Float64()*0.2))
	next := now.Add(delay)
	if failure != nil && failure.after.After(next) {
		next = failure.after
	}
	deadline := jobCreated(created).Add(webhookRetryWindow)
	if next.After(deadline) {
		next = deadline
	}
	return next, false
}
func retryAfter(raw string, now time.Time) time.Time {
	raw = strings.TrimSpace(raw)
	if seconds, e := strconv.ParseInt(raw, 10, 32); e == nil && seconds >= 0 {
		return now.Add(time.Duration(seconds) * time.Second)
	}
	if t, e := http.ParseTime(raw); e == nil && t.After(now) {
		return t
	}
	return time.Time{}
}

func responseError(status int) string {
	if status >= 200 && status < 300 {
		return ""
	}
	return fmt.Sprintf("webhook endpoint returned HTTP %d", status)
}
