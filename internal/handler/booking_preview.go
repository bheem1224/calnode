package handler

import (
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// Drafts travel with each preview request. They are never stored in event_types,
// sessions, or tokens, and cannot authorize the public booking creation path.
type bookingPreviewRequest struct {
	EventType json.RawMessage `json:"event_type"`
	Questions *[]questionJSON `json:"questions,omitempty"`
	Hosts     *[]previewHost  `json:"hosts,omitempty"`
	Mode      string          `json:"mode"`
	Date      string          `json:"date,omitempty"`
	Timezone  string          `json:"timezone,omitempty"`
	Notice    string          `json:"notice,omitempty"`
	Duration  int             `json:"duration,omitempty"`
	Slot      string          `json:"slot,omitempty"`
}

type previewHost struct {
	UserID   string `json:"user_id"`
	Role     string `json:"role"`
	Priority int    `json:"priority"`
}

type bookingPreview struct {
	Request bookingPreviewRequest
	Draft   eventTypeJSON
	Event   *bookableEventType
	Hosts   []EventHost
}

// readBookingPreview applies draft values to a private copy of the saved event.
// Ownership is checked against the URL's saved slug, never any draft identity.
func (h *Handler) readBookingPreview(w http.ResponseWriter, r *http.Request) (*bookingPreview, bool) {
	w.Header().Set("Cache-Control", "no-store")
	user, ok := userFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "unauthorized")
		return nil, false
	}
	slug := r.PathValue("slug")
	etID, ownerID := h.eventTypeForEditor(w, r, slug, user)
	if etID == "" {
		return nil, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	var req bookingPreviewRequest
	var err error
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		if err = r.ParseForm(); err == nil {
			err = json.Unmarshal([]byte(r.PostForm.Get("draft")), &req)
		}
	} else {
		err = json.NewDecoder(r.Body).Decode(&req)
	}
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid preview draft")
		return nil, false
	}
	saved, err := scanEventType(h.db.QueryRowContext(r.Context(), selectETCols+" WHERE id = ?", etID))
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "could not load preview")
		return nil, false
	}
	draft := *saved
	if len(req.EventType) > 0 {
		if err := json.Unmarshal(req.EventType, &draft); err != nil {
			h.writeError(w, http.StatusBadRequest, "invalid event settings")
			return nil, false
		}
	}
	draft.ID, draft.Slug, draft.OwnerID = etID, slug, ownerID
	if err := validatePreviewDraft(draft, req); err != nil {
		h.writeError(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	if draft.ScheduleID != nil && *draft.ScheduleID != "" {
		var exists int
		if err := h.db.QueryRowContext(r.Context(), `SELECT 1 FROM availability_schedules WHERE id = ? AND user_id = ?`, *draft.ScheduleID, ownerID).Scan(&exists); err != nil {
			h.writeError(w, http.StatusBadRequest, "schedule not found")
			return nil, false
		}
	}
	// This is the same model consumed by computeSlots; only its input changes.
	event, err := h.loadEventTypeForBooking(r.Context(), slug, true, true)
	if err != nil {
		h.writeError(w, http.StatusNotFound, "event type not found")
		return nil, false
	}
	event.Name, event.DurationMinutes, event.SlotIntervalMinutes = draft.Name, draft.DurationMinutes, draft.SlotIntervalMinutes
	event.LocationType, event.LocationValue, event.AllowPhoneCall = draft.LocationType, draft.LocationValue, draft.AllowPhoneCall
	event.RoutingMode, event.RRStrategy = draft.RoutingMode, draft.RRStrategy
	event.BufferBeforeMinutes, event.BufferAfterMinutes = draft.BufferBeforeMinutes, draft.BufferAfterMinutes
	event.MinNoticeMinutes, event.MaxFutureDays = draft.MinNoticeMinutes, draft.MaxFutureDays
	event.MaxPerDay, event.MaxPerWeek, event.MaxPerMonth, event.MaxPerYear = draft.MaxPerDay, draft.MaxPerWeek, draft.MaxPerMonth, draft.MaxPerYear
	event.MaxDurationPerDay, event.MaxDurationPerWeek = draft.MaxDurationPerDay, draft.MaxDurationPerWeek
	event.ShowFirstSlotOnly, event.ShowTakenSlots, event.OptimizedSlots = draft.ShowFirstSlotOnly, draft.ShowTakenSlots, draft.OptimizedSlots
	event.FutureLimitMode = draft.FutureLimitMode
	event.FutureRangeStart, event.FutureRangeEnd = stringOrEmpty(draft.FutureRangeStart), stringOrEmpty(draft.FutureRangeEnd)
	event.AllowMultipleDurations, event.DurationOptions = draft.AllowMultipleDurations, draft.DurationOptions
	event.PriceCents, event.Currency = draft.PriceCents, draft.Currency
	hosts, err := h.resolveEventTypeHosts(r.Context(), etID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "could not load preview hosts")
		return nil, false
	}
	if req.Hosts != nil {
		hosts = nil
		seen := map[string]bool{}
		for _, host := range *req.Hosts {
			if seen[host.UserID] || (host.Role != "required" && host.Role != "rotation" && host.Role != "optional") {
				h.writeError(w, http.StatusBadRequest, "invalid preview hosts")
				return nil, false
			}
			var exists int
			if err := h.db.QueryRowContext(r.Context(), `SELECT 1 FROM users WHERE id = ? AND archived_at IS NULL`, host.UserID).Scan(&exists); err != nil {
				h.writeError(w, http.StatusBadRequest, "host not found")
				return nil, false
			}
			seen[host.UserID] = true
			hosts = append(hosts, EventHost{UserID: host.UserID, Role: host.Role, Priority: host.Priority})
		}
		sort.SliceStable(hosts, func(i, j int) bool { return hosts[i].Priority < hosts[j].Priority })
	}
	if req.Mode == "" {
		req.Mode = "calendar"
	}
	if req.Notice == "" {
		req.Notice = "Preview only — bookings are disabled."
	}
	req.EventType, _ = json.Marshal(draft)
	w.Header().Set("Cache-Control", "no-store")
	return &bookingPreview{Request: req, Draft: draft, Event: event, Hosts: hosts}, true
}

func stringOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func validatePreviewDraft(d eventTypeJSON, req bookingPreviewRequest) error {
	// Match the editor API's positive-minute rule, including multi-day events.
	if d.DurationMinutes < 1 || d.SlotIntervalMinutes < 1 {
		return errors.New("duration and slot interval must be positive")
	}
	if d.BufferBeforeMinutes < 0 || d.BufferAfterMinutes < 0 || d.MinNoticeMinutes < 0 || d.MaxFutureDays < 0 {
		return errors.New("invalid scheduling limits")
	}
	if d.RoutingMode != "fixed" && d.RoutingMode != "round_robin" && d.RoutingMode != "collective" {
		return errors.New("invalid routing mode")
	}
	for _, minutes := range d.DurationOptions {
		if minutes < 1 {
			return errors.New("invalid duration option")
		}
	}
	for _, cap := range []*int{d.MaxPerDay, d.MaxPerWeek, d.MaxPerMonth, d.MaxPerYear, d.MaxDurationPerDay, d.MaxDurationPerWeek} {
		if cap != nil && *cap < 0 {
			return errors.New("booking limits cannot be negative")
		}
	}
	if req.Questions != nil {
		seen := map[string]bool{}
		for _, q := range *req.Questions {
			if q.ID == "" || seen[q.ID] || !isValidQuestionType(q.Type) {
				return errors.New("invalid preview question")
			}
			seen[q.ID] = true
		}
	}
	if req.Mode != "" && req.Mode != "calendar" && req.Mode != "form" && req.Mode != "confirmation" {
		return errors.New("invalid preview view")
	}
	return nil
}

// PreviewBookingPage renders the actual public template with unsaved settings.
func (h *Handler) PreviewBookingPage(w http.ResponseWriter, r *http.Request) {
	preview, ok := h.readBookingPreview(w, r)
	if !ok {
		return
	}
	h.renderBookPage(w, r, r.PathValue("slug"), true, bookPrefill{Preview: preview})
}

// PreviewBookingSlots invokes the production engine and live FreeBusy lookup,
// using draft settings. It performs no writes and never creates a booking token.
func (h *Handler) PreviewBookingSlots(w http.ResponseWriter, r *http.Request) {
	preview, ok := h.readBookingPreview(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	want := slotsWanted{Preview: preview, Taken: true, NoticeGap: true}
	if raw := q.Get("duration"); raw != "" {
		minutes, err := strconv.Atoi(raw)
		if err != nil || minutes < 1 {
			h.writeError(w, http.StatusBadRequest, "invalid duration")
			return
		}
		want.DurationMinutes = minutes
	}
	res, err := h.computeSlots(r.Context(), r.PathValue("slug"), q.Get("tz"), q.Get("from"), q.Get("to"), want)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, errBadDateRange) || errors.Is(err, errInvalidTimezone) || errors.Is(err, errInvalidDuration) {
			status = http.StatusBadRequest
		}
		h.writeError(w, status, "could not calculate preview slots")
		return
	}
	h.writeSlotsResult(w, res)
}

func (h *Handler) previewCSP(r *http.Request, slug string) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	origin := (&url.URL{Scheme: scheme, Host: r.Host}).String()
	// A preview can only connect to its read-only slots endpoint. Even a future
	// accidental fetch to /v1/bookings, verification, payment, or chat is blocked.
	return "default-src 'none'; script-src 'unsafe-inline' " + origin + "/phone-input.js; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; connect-src " + origin + "/v1/event-types/" + url.PathEscape(slug) + "/preview/slots; form-action 'none'; base-uri 'none'; frame-ancestors 'self'"
}

func (p *bookingPreview) configJSON(slug string) template.JS {
	config, _ := json.Marshal(map[string]any{
		"draft": p.Request, "mode": p.Request.Mode, "date": p.Request.Date, "timezone": p.Request.Timezone,
		"duration": p.Request.Duration, "slot": p.Request.Slot,
		"slotsURL": "/v1/event-types/" + url.PathEscape(slug) + "/preview/slots",
	})
	return template.JS(config) // #nosec G203 -- json.Marshal escapes HTML-sensitive characters.
}
