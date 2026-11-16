package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// runSameOrigin sends r through SameOriginCheck and returns (statusCode, nextCalled).
func runSameOrigin(r *http.Request) (int, bool) {
	called := false
	h := SameOriginCheck(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec.Code, called
}

func csrfReq(method, origin string, withCookie bool) *http.Request {
	r := httptest.NewRequest(method, "http://app.example.com/v1/teams", nil)
	r.Host = "app.example.com"
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if withCookie {
		r.AddCookie(&http.Cookie{Name: "calnode_session", Value: "sess"})
	}
	return r
}

func TestSameOrigin_blocksCrossOriginCookieWrite(t *testing.T) {
	code, called := runSameOrigin(csrfReq(http.MethodPost, "http://evil.example.net", true))
	if code != http.StatusForbidden {
		t.Errorf("status = %d; want 403", code)
	}
	if called {
		t.Error("next handler should not run for a blocked cross-origin write")
	}
}

func TestSameOrigin_allowsSameOriginCookieWrite(t *testing.T) {
	code, called := runSameOrigin(csrfReq(http.MethodPost, "http://app.example.com", true))
	if code != http.StatusOK || !called {
		t.Errorf("same-origin write should pass: status=%d called=%v", code, called)
	}
}

func TestSameOrigin_allowsWriteWithoutSessionCookie(t *testing.T) {
	// Public booking POST / API-key clients carry no session cookie — never blocked.
	code, called := runSameOrigin(csrfReq(http.MethodPost, "http://evil.example.net", false))
	if code != http.StatusOK || !called {
		t.Errorf("no-cookie write should pass: status=%d called=%v", code, called)
	}
}

func TestSameOrigin_allowsGetEvenCrossOrigin(t *testing.T) {
	code, called := runSameOrigin(csrfReq(http.MethodGet, "http://evil.example.net", true))
	if code != http.StatusOK || !called {
		t.Errorf("GET should pass regardless of origin: status=%d called=%v", code, called)
	}
}

func TestSameOrigin_allowsWhenNoOriginOrReferer(t *testing.T) {
	// Neither header present → can't determine cross-origin; SameSite=Lax is the guard.
	code, called := runSameOrigin(csrfReq(http.MethodPost, "", true))
	if code != http.StatusOK || !called {
		t.Errorf("missing Origin/Referer should pass: status=%d called=%v", code, called)
	}
}

func TestSameOrigin_fallsBackToReferer(t *testing.T) {
	r := csrfReq(http.MethodDelete, "", true) // no Origin
	r.Header.Set("Referer", "http://evil.example.net/some/page")
	code, called := runSameOrigin(r)
	if code != http.StatusForbidden || called {
		t.Errorf("cross-origin Referer should block: status=%d called=%v", code, called)
	}
}

func TestSameOriginWithHosts_allowsConfiguredBaseHostAndTrustedProxy(t *testing.T) {
	// 1. Rewritten Host header (e.g. 127.0.0.1:8080) allowed when Origin matches configured BaseURL host.
	h := SameOriginCheckWithHosts([]string{"cal.example.com"}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	r1 := httptest.NewRequest(http.MethodPatch, "http://127.0.0.1:8080/v1/settings/microsoft", nil)
	r1.Host = "127.0.0.1:8080"
	r1.Header.Set("Origin", "https://cal.example.com")
	r1.AddCookie(&http.Cookie{Name: "calnode_session", Value: "sess"})
	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, r1)
	if rec1.Code != http.StatusOK {
		t.Errorf("configured base host should allow rewritten Host: got %d", rec1.Code)
	}

	// 2. Trusted proxy X-Forwarded-Host allowed when TrustClientIP marks peer as trusted.
	trusted, err := ParseTrustedProxies([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatalf("ParseTrustedProxies: %v", err)
	}
	h2 := TrustClientIP(trusted)(SameOriginCheckWithHosts(nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))
	r2 := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/v1/bookings", nil)
	r2.RemoteAddr = "10.0.0.5:54321"
	r2.Host = "127.0.0.1:8080"
	r2.Header.Set("X-Forwarded-Host", "proxy.example.com")
	r2.Header.Set("Origin", "https://proxy.example.com")
	r2.AddCookie(&http.Cookie{Name: "calnode_session", Value: "sess"})
	rec2 := httptest.NewRecorder()
	h2.ServeHTTP(rec2, r2)
	if rec2.Code != http.StatusOK {
		t.Errorf("trusted proxy X-Forwarded-Host should allow: got %d", rec2.Code)
	}

	// 3. Untrusted peer spoofing X-Forwarded-Host is still blocked.
	r3 := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/v1/bookings", nil)
	r3.RemoteAddr = "203.0.113.9:54321"
	r3.Host = "127.0.0.1:8080"
	r3.Header.Set("X-Forwarded-Host", "evil.example.com")
	r3.Header.Set("Origin", "https://evil.example.com")
	r3.AddCookie(&http.Cookie{Name: "calnode_session", Value: "sess"})
	rec3 := httptest.NewRecorder()
	h2.ServeHTTP(rec3, r3)
	if rec3.Code != http.StatusForbidden {
		t.Errorf("untrusted peer X-Forwarded-Host must be blocked: got %d", rec3.Code)
	}
}
