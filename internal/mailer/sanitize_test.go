package mailer

import (
	"strings"
	"testing"
)

func TestSanitizeAndValidateEmailHTML_Bluemonday(t *testing.T) {
	// 1. Preserves safe HTML and {ManageURL} / {JoinURL} placeholders in hrefs.
	in := `<div style="color:#111827;padding:12px"><h1>Hi {Scheduler}</h1><p><a href="{ManageURL}">Reschedule</a> <a href="{JoinURL}">Join</a></p></div>`
	out, err := SanitizeAndValidateEmailHTML(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{`href="{ManageURL}"`, `href="{JoinURL}"`, "Hi {Scheduler}"} {
		if !strings.Contains(out, want) {
			t.Errorf("sanitized output missing %q:\n%s", want, out)
		}
	}

	// 2. Strips scripts, styles, event handlers, and unquoted/encoded javascript URIs.
	evil := `<style>body{display:none}</style><script>alert(1)</script><p onclick="alert(2)">Hello <a href=javascript:alert(3)>click</a> <img src="x" onerror=alert(4)></p>`
	cleaned, err := SanitizeAndValidateEmailHTML(evil)
	if err != nil {
		t.Fatalf("unexpected error on mixed HTML: %v", err)
	}
	for _, bad := range []string{"<script", "<style", "onclick", "onerror", "javascript:"} {
		if strings.Contains(strings.ToLower(cleaned), bad) {
			t.Errorf("sanitized output still contains %q:\n%s", bad, cleaned)
		}
	}

	// 3. InterpolateTemplate HTML-escapes {JoinURL} and {ManageURL} when htmlEscape=true.
	interp := InterpolateTemplate(`<a href="{JoinURL}">Join</a>`, BookingData{
		LocationValue: `https://meet.example.com/abc" onclick="alert(1)`,
	}, true)
	if strings.Contains(interp, `" onclick="`) {
		t.Errorf("InterpolateTemplate did not HTML-escape JoinURL: %s", interp)
	}
}
