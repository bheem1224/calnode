package mailer

import (
	"errors"
	"fmt"
	"html"
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
)

const maxEmailHTMLBytes = 64 << 10

var (
	reStripAllTags = regexp.MustCompile(`(?s)<[^>]*>`)
	emailPolicy    = buildEmailHTMLPolicy()
)

// buildEmailHTMLPolicy constructs a strict bluemonday allowlist policy for
// operator-authored HTML email templates: permits standard email layout tags,
// links/images with http/https/mailto schemes, and safe inline presentation
// styles, while stripping scripts, iframes, forms, SVGs, <style> blocks,
// event handlers, and dangerous URI schemes using an HTML5 parser.
func buildEmailHTMLPolicy() *bluemonday.Policy {
	p := bluemonday.NewPolicy()

	p.AllowStandardURLs()
	p.AllowURLSchemes("http", "https", "mailto")

	// Text & block formatting
	p.AllowElements(
		"p", "br", "div", "span", "hr",
		"h1", "h2", "h3", "h4", "h5", "h6",
		"strong", "b", "em", "i", "u", "s", "small", "sub", "sup",
		"ul", "ol", "li", "blockquote", "pre", "code", "center",
	)

	// Email layout tables
	p.AllowElements("table", "thead", "tbody", "tfoot", "tr", "th", "td")
	p.AllowAttrs("role", "border", "cellpadding", "cellspacing", "width", "height", "align", "valign", "bgcolor").
		OnElements("table", "thead", "tbody", "tfoot", "tr", "th", "td")
	p.AllowAttrs("colspan", "rowspan").OnElements("th", "td")

	// Links & images
	p.AllowAttrs("href", "title", "target").OnElements("a")
	p.AllowAttrs("src", "alt", "title", "width", "height", "border").OnElements("img")

	// Safe inline presentation styles for HTML email layout (no url(), expression(), etc.)
	safeCSSVal := regexp.MustCompile(`(?i)^[^;()\\]+$`)
	p.AllowStyles(
		"color", "background", "background-color",
		"font", "font-family", "font-size", "font-weight", "font-style",
		"line-height", "letter-spacing", "text-align", "text-decoration", "text-transform", "vertical-align",
		"margin", "margin-top", "margin-right", "margin-bottom", "margin-left",
		"padding", "padding-top", "padding-right", "padding-bottom", "padding-left",
		"border", "border-top", "border-right", "border-bottom", "border-left",
		"border-color", "border-width", "border-style", "border-radius", "border-collapse",
		"width", "max-width", "min-width", "height", "max-height", "min-height",
		"display", "opacity", "white-space", "word-break", "overflow-wrap",
	).Matching(safeCSSVal).Globally()

	return p
}

// SanitizeAndValidateEmailHTML sanitizes operator-authored email HTML using
// bluemonday's HTML5 allowlist parser and validates that the resulting template
// is non-empty and within the 64 KB size limit.
func SanitizeAndValidateEmailHTML(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("HTML template cannot be empty")
	}
	if len(trimmed) > maxEmailHTMLBytes {
		return "", errors.New("HTML template exceeds 64 KB limit")
	}

	const (
		manageSentinel = "https://calnode-placeholder.invalid/__manage_url__"
		joinSentinel   = "https://calnode-placeholder.invalid/__join_url__"
	)
	prepped := strings.NewReplacer(
		"{ManageURL}", manageSentinel,
		"{JoinURL}", joinSentinel,
	).Replace(trimmed)

	cleaned := strings.TrimSpace(emailPolicy.Sanitize(prepped))
	cleaned = strings.NewReplacer(
		manageSentinel, "{ManageURL}",
		joinSentinel, "{JoinURL}",
	).Replace(cleaned)

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
		"{ManageURL}", esc(d.ManageURL),
		"{JoinURL}", esc(d.LocationValue),
	)
	return r.Replace(tmpl)
}
