package handler

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/i18n"
	"github.com/calnode/calnode/internal/mailer"
	"github.com/calnode/calnode/internal/webhook"
)

//go:embed templates/manage.html
var manageTmplSrc string

// Shared chrome partials (consent/tracking/footer) are parsed first so manage.html can
// reference them via {{template "trackingHead" .}} etc. — same source as the booking page.
var manageTmpl = template.Must(template.Must(template.New("manage").Funcs(template.FuncMap{
	"supportedLocales": i18n.SupportedLocales,
}).Parse(sharedPartialsSrc)).Parse(manageTmplSrc))

type managePageData struct {
	AccentColor      string
	AccentForeground string
	Token            string
	BookingID        string
	EventTypeName    string
	EventDescription string
	CalendarLocation string
	EventTypeSlug    string
	HostName         string
	HostInitial      string
	AvatarURL        string
	// SoleHostName is the host's name when this booking has exactly one, else "" — see
	// bookPageData.SoleHostName. HostName can be a group label ("Alex, Sam & 2 others"),
	// which no "%s has no available times" sentence can use grammatically.
	SoleHostName string
	// MinNoticeLabel is the translated minimum-notice duration ("4 hours") of the event
	// type being rescheduled, or "" when it sets none. Reschedule goes through the same
	// /slots endpoint as booking, so the same policy hides the same nearest times (#20).
	MinNoticeLabel      string
	DurationLabel       string
	LocationLabel       string
	PriceLabel          string // empty on manage → the eventMeta partial omits the price row
	MaxFutureDays       int
	DurationMinutes     int
	CurrentStartISO     string // RFC3339 for JS
	OrganizerTZ         string
	Status              string // "confirmed" or "cancelled"
	TokenInvalid        bool   // token not found or expired
	CanCancel           bool
	CanReschedule       bool
	RequireCancelReason string // "none", "optional", "required"
	// Tracking
	HeadHTML         template.HTML
	DataLayerEnabled bool
	DataLayerFields  template.JS
	GTMContainerID   string // native GTM container; consent-gated (shared trackingHead/consentBanner)
	GA4MeasurementID string // native GA4 id; consent-gated
	// Branding
	BusinessName  string
	LogoURL       string
	LogoHeight    int
	LogoOpacity   string // CSS opacity value, e.g. "1" or "0.6"
	BannerURL     string
	BannerOpacity string // CSS opacity value, e.g. "1" or "0.6"
	PrivacyURL    string // operator Privacy Policy URL (legalFooter + banner link)
	TermsURL      string // operator Terms URL (legalFooter)
	CSSVersion    string // cache-busts the /booking.css link (content hash)
	// BookingLogicJS is the shared booking-calendar logic module, inlined ahead of the page script.
	BookingLogicJS template.JS
	// DemoMode shows the "public demo" banner + a noindex meta tag (see internal/demo).
	DemoMode bool
	// Locale/T/I18NJSON — same pattern as bookPageData: T resolves a single translation
	// key server-side; I18NJSON is the same locale's full string table, injected as
	// window.__CALNODE_I18N for this page's own JS (reschedule/cancel flow).
	Locale   string
	T        func(string) string
	I18NJSON template.JS
}

// ManagePage renders the attendee manage page for a booking (reschedule / cancel).
func (h *Handler) ManagePage(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")

	b, err := h.bookingSvc.ValidateManageToken(r.Context(), token)
	if errors.Is(err, booking.ErrTokenNotFound) {
		h.renderManage(w, r, managePageData{TokenInvalid: true}, h.resolveLocale(r))
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "manage page: validate token", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	var etName, etSlug, eventDescription, locType, locValue, requireCancelReason string
	var durMins, maxDays, minNotice int
	var disableCancel, disableReschedule, allowReschedulePast int
	var hostName, accentColor string
	if err := h.db.QueryRowContext(r.Context(), `
		SELECT et.name, et.slug, COALESCE(et.description,''), et.duration_minutes, et.max_future_days, et.min_notice_minutes,
		       et.location_type, COALESCE(et.location_value,''),
		       et.disable_cancel, et.disable_reschedule, et.require_cancel_reason, et.allow_reschedule_past,
		       u.name, u.booking_accent
		FROM event_types et JOIN users u ON u.id = et.user_id
		WHERE et.id = ?`, b.EventTypeID).
		Scan(&etName, &etSlug, &eventDescription, &durMins, &maxDays, &minNotice, &locType, &locValue,
			&disableCancel, &disableReschedule, &requireCancelReason, &allowReschedulePast,
			&hostName, &accentColor); err != nil {
		h.logger.ErrorContext(r.Context(), "manage page: load event type", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	accentColor = accentOrDefault(accentColor)

	if b.LocationType != "" {
		locType = b.LocationType
	}
	if b.LocationValue != "" {
		locValue = b.LocationValue
	}

	// Show the actual assigned host(s) for this booking, not the event-type owner
	// (round-robin/Group route elsewhere). Falls back to the owner name above if
	// no booking_hosts rows exist. The avatar uses the primary host.
	loc := h.resolveLocale(r)
	var hostInitial, avatarURL, soleHost string
	if hosts := h.displayHostsForBooking(r.Context(), b.ID); len(hosts) > 0 {
		hostName = hostsLabel(hosts, loc)
		hostInitial = hosts[0].Initial
		avatarURL = hosts[0].AvatarURL
		if len(hosts) == 1 {
			soleHost = hosts[0].Name
		}
	} else {
		hostInitial = firstRune(hostName)
		soleHost = hostName // the event-type owner: one person, so nameable
	}

	var orgTZ string
	_ = h.db.QueryRowContext(r.Context(), `
		SELECT iana_timezone FROM booking_attendees
		WHERE booking_id = ? AND is_organizer = 1`, b.ID).Scan(&orgTZ)
	if orgTZ == "" {
		orgTZ = "UTC"
	}

	data := managePageData{
		AccentColor:         accentColor,
		AccentForeground:    accentForeground(accentColor),
		Token:               token,
		BookingID:           b.ID,
		EventTypeName:       etName,
		EventDescription:    eventDescription,
		CalendarLocation:    locValue,
		EventTypeSlug:       etSlug,
		HostName:            hostName,
		HostInitial:         hostInitial,
		AvatarURL:           avatarURL,
		SoleHostName:        soleHost,
		MinNoticeLabel:      noticeLabel(minNotice, loc),
		DurationLabel:       durationLabel(durMins, loc),
		LocationLabel:       locationLabel(locType, locValue, loc),
		MaxFutureDays:       maxDays,
		DurationMinutes:     durMins,
		CurrentStartISO:     b.StartAt.UTC().Format(time.RFC3339),
		OrganizerTZ:         orgTZ,
		Status:              b.Status,
		CanCancel:           disableCancel == 0,
		CanReschedule:       disableReschedule == 0 && (allowReschedulePast == 1 || !b.StartAt.Before(bookingNow().UTC())),
		RequireCancelReason: requireCancelReason,
	}
	h.renderManage(w, r, data, loc)
}

// renderManage finishes populating data (tracking/branding/locale) and executes the
// template. loc is the request's already-resolved locale — the caller resolves it (a DB
// read for the operator's fallback setting), so this doesn't redundantly re-resolve it.
func (h *Handler) renderManage(w http.ResponseWriter, r *http.Request, data managePageData, loc *i18n.Locale) {
	track := h.loadTrackingSettings(r.Context())
	dlFields, _ := json.Marshal(track.DataLayerFields)
	data.HeadHTML = template.HTML(track.HeadHTML) // #nosec G203 -- admin-only "code injection" feature (Settings -> Tracking); intentionally raw, documented, gated by requireAdmin on the settings endpoint
	data.DataLayerEnabled = track.DataLayerEnabled
	data.DataLayerFields = template.JS(dlFields) // #nosec G203 -- json.Marshal output, which escapes <,>,& by default; safe for embedding in a <script> block
	data.GTMContainerID = track.GTMContainerID
	data.GA4MeasurementID = track.GA4MeasurementID
	brand := h.loadBranding(r.Context())
	data.BusinessName = brand.BusinessName
	data.LogoURL = brand.LogoURL
	data.LogoHeight = pageLogoHeight(brand.LogoHeight)
	data.LogoOpacity = opacityCSS(brand.LogoOpacity)
	data.BannerURL = brand.BannerURL
	data.BannerOpacity = opacityCSS(brand.BannerOpacity)
	data.PrivacyURL = brand.PrivacyURL
	data.TermsURL = brand.TermsURL
	data.CSSVersion = bookingCSSVersion
	data.BookingLogicJS = template.JS(bookingLogicJS) // #nosec G203 -- our own bundled JS source constant, not user input
	data.DemoMode = h.demoMode
	data.Locale = loc.Code
	data.T = loc.T
	i18nJSON, _ := loc.JSON()
	data.I18NJSON = template.JS(i18nJSON) // #nosec G203 -- json.Marshal output, which escapes <,>,& by default; safe for embedding in a <script> block

	h.persistLangOverride(w, r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", publicCSP(track))
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Vary", "Accept-Language, Cookie") // see the same header in book.go's BookPage
	if err := manageTmpl.Execute(w, data); err != nil {
		h.logger.ErrorContext(r.Context(), "manage page: template", "error", err)
	}
}

// RescheduleByToken moves a booking to a new time authenticated by a manage token.
// POST /manage/{token}/reschedule  body: {"start_at":"<RFC3339>"}
func (h *Handler) RescheduleByToken(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)

	var req struct {
		StartAt string `json:"start_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.StartAt == "" {
		h.writeError(w, http.StatusBadRequest, "start_at is required")
		return
	}
	newStart, err := time.Parse(time.RFC3339, req.StartAt)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "start_at must be RFC3339")
		return
	}

	b, err := h.bookingSvc.ValidateManageToken(r.Context(), token)
	if errors.Is(err, booking.ErrTokenNotFound) {
		h.writeError(w, http.StatusNotFound, "manage link not found or expired")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "reschedule: validate token", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	var durMins, disableReschedule, allowReschedulePast int
	if err := h.db.QueryRowContext(r.Context(),
		`SELECT duration_minutes, disable_reschedule, allow_reschedule_past FROM event_types WHERE id = ?`, b.EventTypeID).
		Scan(&durMins, &disableReschedule, &allowReschedulePast); err != nil {
		h.logger.ErrorContext(r.Context(), "reschedule: load duration", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if disableReschedule == 1 {
		h.writeError(w, http.StatusForbidden, "rescheduling is disabled for this event type")
		return
	}
	if allowReschedulePast == 0 && b.StartAt.Before(bookingNow().UTC()) {
		h.writeError(w, http.StatusForbidden, "past bookings cannot be rescheduled")
		return
	}

	previousStart := b.StartAt
	previousEnd := b.EndAt
	newEnd := newStart.Add(time.Duration(durMins) * time.Minute)

	if err := h.validateRescheduleTime(r.Context(), b.ID, b.EventTypeID, b.HostID, newStart, newEnd); err != nil {
		if errors.Is(err, errSlotUnavailable) {
			h.writeError(w, http.StatusConflict, "that time slot is no longer available")
			return
		}
		h.logger.ErrorContext(r.Context(), "reschedule: validate time", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	updated, err := h.bookingSvc.Reschedule(r.Context(), b.ID, newStart, newEnd)
	if errors.Is(err, booking.ErrDoubleBooked) {
		h.writeError(w, http.StatusConflict, "that time slot is no longer available")
		return
	}
	if errors.Is(err, booking.ErrFrequencyCapReached) {
		h.writeError(w, http.StatusConflict, "this event type has reached its booking limit for that day or week")
		return
	}
	if errors.Is(err, booking.ErrAlreadyCancelled) {
		h.writeError(w, http.StatusConflict, "this booking has been cancelled")
		return
	}
	if errors.Is(err, booking.ErrNotFound) {
		h.writeError(w, http.StatusNotFound, "booking not found")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "reschedule: update", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	response := toBookingJSON(updated)
	rotated, err := h.bookingSvc.RotateManageToken(r.Context(), updated.ID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "reschedule: rotate manage token", "error", err)
	} else {
		response.ManageURL = h.publicURL() + "/manage/" + rotated
	}
	h.writeJSON(w, http.StatusOK, response)

	go h.rescheduleSideEffects(*updated, b.EventTypeID, previousStart, previousEnd, rotated) // #nosec G118 -- deliberately its own context.Background(); see rescheduleSideEffects' doc comment
}

// rescheduleSideEffects moves the calendar event(s) to the new time, rotates the
// manage token, emails attendee + host, fires the booking.rescheduled webhook, and
// reschedules reminders. Intended to run in its own goroutine; every failure is
// logged, never fatal. Shared by the manage-link RescheduleByToken handler and the
// MCP reschedule_booking tool.
func (h *Handler) rescheduleSideEffects(bCopy booking.Booking, capturedEtID string, previousStart, previousEnd time.Time, rotatedToken ...string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// LiveKit join URLs expire with the original meeting: re-mint them first so the
	// email data loaded below, the manage page, and the stored record all carry
	// links valid past the new end (#98). Same room, new expiry.
	h.remintLiveKitLinks(ctx, &bCopy)

	d, err := h.loadCancellationData(ctx, &bCopy)
	if err != nil {
		h.logger.Error("reschedule: load email data", "error", err, "booking_id", bCopy.ID)
		return
	}
	d.BaseURL = h.publicURL()
	d.PreviousStartAt = previousStart
	d.PreviousEndAt = previousEnd
	h.applyBranding(ctx, &d)

	// Move the calendar event(s) to the new time (all hosts, for Group bookings).
	h.moveCalendarEvents(ctx, bCopy.ID, bCopy.StartAt, bCopy.EndAt)
	// Update the Zoom meeting time too (the join URL is unchanged).
	h.rescheduleZoomMeeting(ctx, &bCopy)

	// Rotate the token so the original confirmation-email link is invalidated.
	if len(rotatedToken) > 0 && rotatedToken[0] != "" {
		d.ManageURL = h.publicURL() + "/manage/" + rotatedToken[0]
	} else if tok, err := h.bookingSvc.RotateManageToken(ctx, bCopy.ID); err == nil {
		d.ManageURL = h.publicURL() + "/manage/" + tok
	}
	h.applyInviteDelivery(ctx, &d, bCopy.InviteDelivery, bCopy.HostID)
	d.ICSSequence = int(bCopy.UpdatedAt.Unix())

	prefs := h.hostPrefsOrDefault(ctx, bCopy.ID, bCopy.HostID)
	var msgNote, subjNote, replyTo sql.NullString
	_ = h.db.QueryRowContext(ctx, `SELECT msg_reschedule, subj_reschedule, reply_to FROM event_types WHERE id = ?`, capturedEtID).
		Scan(&msgNote, &subjNote, &replyTo)
	if msgNote.Valid {
		d.CustomNote = msgNote.String
	}
	if subjNote.Valid {
		d.SubjectOverride = subjNote.String
	}
	if replyTo.Valid {
		d.ReplyTo = replyTo.String
	}
	if prefs.NotifyReschedule {
		if err := mailer.SendRescheduleToAttendee(ctx, h.mailer, d); err != nil {
			h.logger.Error("reschedule email (attendee)", "error", err, "booking_id", bCopy.ID)
		}
	}
	if prefs.NotifyHostReschedule {
		hd := d
		h.applyHostInvite(ctx, &hd, bCopy.InviteDelivery, bCopy.HostID)
		if err := mailer.SendRescheduleToHost(ctx, h.mailer, hd); err != nil {
			h.logger.Error("reschedule email (host)", "error", err, "booking_id", bCopy.ID)
		}
	}

	if h.webhookSvc != nil {
		if err := h.webhookSvc.Enqueue(ctx, "booking.rescheduled", webhook.BookingPayload{
			ID:              bCopy.ID,
			EventTypeSlug:   d.EventTypeSlug,
			HostID:          bCopy.HostID,
			StartAt:         bCopy.StartAt.UTC().Format(time.RFC3339),
			EndAt:           bCopy.EndAt.UTC().Format(time.RFC3339),
			Status:          bCopy.Status,
			LocationValue:   bCopy.LocationValue,
			CreatedAt:       bCopy.CreatedAt.UTC().Format(time.RFC3339),
			PreviousStartAt: previousStart.UTC().Format(time.RFC3339),
			PreviousEndAt:   previousEnd.UTC().Format(time.RFC3339),
		}); err != nil {
			h.logger.Error("enqueue booking.rescheduled webhook", "error", err, "booking_id", bCopy.ID)
		}
	}

	if err := h.replaceReminderJobs(ctx, bCopy.ID, capturedEtID, bCopy.StartAt); err != nil {
		h.logger.Error("reschedule: replace reminder jobs", "error", err, "booking_id", bCopy.ID)
	}
}

// CancelByToken cancels a booking authenticated by a manage token.
// POST /manage/{token}/cancel  body: {"reason":"<optional>"}
func (h *Handler) CancelByToken(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)

	var req struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req) // reason is optional; ignore decode errors

	existing, err := h.bookingSvc.ValidateManageToken(r.Context(), token)
	if errors.Is(err, booking.ErrTokenNotFound) {
		h.writeError(w, http.StatusNotFound, "manage link not found or expired")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "cancel by token: validate", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	var disableCancel int
	var requireCancelReason string
	if err := h.db.QueryRowContext(r.Context(),
		`SELECT disable_cancel, require_cancel_reason FROM event_types WHERE id = ?`, existing.EventTypeID).
		Scan(&disableCancel, &requireCancelReason); err != nil {
		h.logger.ErrorContext(r.Context(), "cancel by token: load policy", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if disableCancel == 1 {
		h.writeError(w, http.StatusForbidden, "cancellation is disabled for this event type")
		return
	}
	reason := strings.TrimSpace(req.Reason)
	switch requireCancelReason {
	case "required", "both", "attendee_only":
		if reason == "" {
			h.writeError(w, http.StatusBadRequest, "cancellation reason is required")
			return
		}
	case "none":
		reason = ""
	}

	b, err := h.bookingSvc.CancelByToken(r.Context(), token, reason)
	if errors.Is(err, booking.ErrTokenNotFound) {
		h.writeError(w, http.StatusNotFound, "manage link not found or expired")
		return
	}
	if errors.Is(err, booking.ErrAlreadyCancelled) {
		h.writeError(w, http.StatusConflict, "this booking is already cancelled")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "cancel by token", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.writeJSON(w, http.StatusOK, toBookingJSON(b))

	// Same multi-host fan-out as the admin cancel path (Group bookings remove the
	// event from every assigned host's calendar and notify each).
	go h.cancelSideEffects(*b) // #nosec G118 -- deliberately its own context.Background(); see cancelSideEffects' doc comment
}
