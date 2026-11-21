package worker

import (
	"testing"
	"time"
)

func TestWebhookRetryWindowJitterAndRetryAfter(t *testing.T) {
	now := time.Now().UTC()
	created := now.Format(time.RFC3339Nano)
	for _, attempt := range []int{1, 2, 8, 23} {
		next, terminal := retryAt("webhook.deliver", created, attempt, &deliveryFailure{status: 503}, now)
		if terminal || !next.After(now) || next.After(now.Add(webhookRetryWindow)) {
			t.Fatal("invalid retry", attempt, next, terminal)
		}
	}
	wait := now.Add(2 * time.Hour)
	next, terminal := retryAt("webhook.deliver", created, 1, &deliveryFailure{status: 429, after: wait}, now)
	if terminal || next.Before(wait) {
		t.Fatal("ignored Retry-After")
	}
	for _, code := range []int{400, 401, 403, 404, 410, 422} {
		_, terminal = retryAt("webhook.deliver", created, 1, &deliveryFailure{status: code}, now)
		if !terminal {
			t.Fatal("permanent failure retried", code)
		}
	}
	for _, code := range []int{408, 425, 429, 500, 503} {
		_, terminal = retryAt("webhook.deliver", created, 1, &deliveryFailure{status: code}, now)
		if terminal {
			t.Fatal("transient failure abandoned", code)
		}
	}
	if _, terminal = retryAt("webhook.enqueue", now.Add(-73*time.Hour).Format(time.RFC3339Nano), 1, &deliveryFailure{status: 503}, now); !terminal {
		t.Fatal("retry window not enforced")
	}
	next, terminal = retryAt("webhook.deliver", created, 1, &deliveryFailure{status: 429, after: now.Add(100 * time.Hour)}, now)
	if terminal || !next.Equal(now.Add(webhookRetryWindow)) {
		t.Fatal("Retry-After escaped retry window")
	}
	if retryAfter("120", now).Sub(now) != 2*time.Minute || !retryAfter("garbage", now).IsZero() {
		t.Fatal("invalid Retry-After parsing")
	}
}
