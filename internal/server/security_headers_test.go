package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSensitiveRouteLogRedaction(t *testing.T) {
	for _, path := range []string{"/manage/secret", "/room/secret", "/v1/invites/secret", "/admin/invite/secret", "/t/secret", "/v1/single-use-links/secret", "/v1/public/group/alice+bob/bookings"} {
		got := redactTokenPaths(path)
		if !strings.Contains(got, "[redacted]") || strings.Contains(got, "secret") || strings.Contains(got, "alice") {
			t.Errorf("sensitive path retained: %s", got)
		}
	}
	if got := redactTokenPaths("/admin/settings/security"); got != "/admin/settings/security" {
		t.Errorf("ordinary path changed: %s", got)
	}
}
func TestSecurityHeadersPrivateResponses(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	for _, path := range []string{"/v1/users/me", "/t/token", "/manage/token"} {
		out := httptest.NewRecorder()
		SecurityHeaders(true, next).ServeHTTP(out, httptest.NewRequest("GET", path, nil))
		for key, want := range map[string]string{"X-Content-Type-Options": "nosniff", "Referrer-Policy": "no-referrer", "Cache-Control": "no-store"} {
			if got := out.Header().Get(key); got != want {
				t.Errorf("%s %s=%q want %q", path, key, got, want)
			}
		}
		if out.Header().Get("Strict-Transport-Security") == "" {
			t.Fatal("HTTPS transport policy missing")
		}
	}
}
