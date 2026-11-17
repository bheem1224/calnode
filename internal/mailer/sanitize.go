package mailer

import (
	"errors"
	"fmt"
	"html"
	"regexp"
	"strings"
)

const maxEmailHTMLBytes = 64 << 10

var dangerousBlockRegexes = func() []*regexp.Regexp {
	tags := []string{"script", "iframe", "object", "embed", "applet", "form", "svg"}
	out := make([]*regexp.Regexp, 0, len(tags))
	for _, tag := range tags {
		out = append(out, regexp.MustCompile(`(?is)<\s*`+tag+`\b[^>]*>.*?<\s*/\s*`+tag+`\s*>`))
	}
	return out
}()

var (
	reDangerousVoid = regexp.MustCompile(`(?is)<\s*/?\s*(script|iframe|object|embed|applet|form|input|button|select|textarea|meta|link|base|svg)\b[^>]*>`)
	reEventAttr     = regexp.MustCompile(`(?is)\s+on[a-z]+\s*=\s*(?:"[^"]*"|'[^']*'|[^\s>]+)`)
	reJSURLAttr     = regexp.MustCompile(`(?is)(\s+(?:href|src|action|formaction)\s*=\s*)(?:"\s*(?:javascript|vbscript|data\s*:\s*text/html)[^"]*"|'\s*(?:javascript|vbscript|data\s*:\s*text/html)[^']*')`)
	reStripAllTags  = regexp.MustCompile(`(?s)<[^>]*>`)
)

// SanitizeAndValidateEmailHTML strips active/dangerous HTML constructs (<script>,
// <iframe>, <form>, event handlers, javascript: URIs) and validates that the
// resulting template is non-empty and within the 64 KB size limit.
func SanitizeAndValidateEmailHTML(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("HTML template cannot be empty")
	}
	if len(trimmed) > maxEmailHTMLBytes {
		return "", errors.New("HTML template exceeds 64 KB limit")
	}

	cleaned := trimmed
	for _, re := range dangerousBlockRegexes {
		cleaned = re.ReplaceAllString(cleaned, "")
	}
	cleaned = reDangerousVoid.ReplaceAllString(cleaned, "")
	cleaned = reEventAttr.ReplaceAllString(cleaned, "")
	cleaned = reJSURLAttr.ReplaceAllString(cleaned, `$1"#"`)

	// Ensure there is actual visible text or image content after stripping tags.
	textOnly := strings.TrimSpace(reStripAllTags.ReplaceAllString(cleaned, ""))
	if textOnly == "" && !strings.Contains(strings.ToLower(cleaned), "<img") {
		return "", errors.New("HTML template has no visible content after sanitization")
	}
	return cleaned, nil
}

// InterpolateTemplate substitutes supported booking variables ({Event}, {Duration},
// {Host}, {HostFirst}, {Scheduler}, {SchedulerFirst}, {Location}, {StartTime},
// {ManageURL}, {JoinURL}) into a subject or HTML/text template.
func InterpolateTemplate(tmpl string, d BookingData, htmlEscape bool) string {
	if tmpl == "" {
		return ""
	}
	esc := func(s string) string {
		if htmlEscape {
			return html.EscapeString(s)
		}
		return s
	}
	firstWord := func(s string) string {
		parts := strings.Fields(strings.TrimSpace(s))
		if len(parts) == 0 {
			return ""
		}
		return parts[0]
	}
	dur := ""
	if !d.EndAt.IsZero() && !d.StartAt.IsZero() {
		mins := int(d.EndAt.Sub(d.StartAt).Minutes())
		if mins > 0 {
			dur = fmt.Sprintf("%dm", mins)
		}
	}
	startFmt := ""
	if !d.StartAt.IsZero() {
		startFmt = inTZ(d.StartAt, d.OrganizerTimezone, d.locale())
	}

	r := strings.NewReplacer(
		"{Event}", esc(d.EventTypeName),
		"{Duration}", esc(dur),
		"{Host}", esc(d.HostName),
		"{HostFirst}", esc(firstWord(d.HostName)),
		"{Scheduler}", esc(d.OrganizerName),
		"{SchedulerFirst}", esc(firstWord(d.OrganizerName)),
		"{Location}", esc(d.LocationValue),
		"{StartTime}", esc(startFmt),
		"{ManageURL}", d.ManageURL,
		"{JoinURL}", d.LocationValue,
	)
	return r.Replace(tmpl)
}
