package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/i18n"
	"github.com/calnode/calnode/internal/mailer"
)

// ListUserUpcomingBookings handles GET /v1/users/{id}/upcoming-bookings (admin).
// Returns the member's upcoming, non-cancelled bookings as host — the list that
// drives the "resolve meetings" step before archiving.
func (h *Handler) ListUserUpcomingBookings(w http.ResponseWriter, r *http.Request) {
	actor, ok := userFromContext(r.Context())
	if !ok || !actor.IsAdmin {
		h.writeError(w, http.StatusForbidden, "admin access required")
		return
	}
	hostID := r.PathValue("id")
	now := time.Now().UTC().Format(time.RFC3339Nano)

	rows, err := h.db.QueryContext(r.Context(), `
		SELECT b.id, b.start_at, b.end_at, et.name, et.slug,
		       COALESCE(a.name,''), COALESCE(a.email,'')
		FROM bookings b
		JOIN event_types et ON et.id = b.event_type_id
		LEFT JOIN booking_attendees a ON a.booking_id = b.id AND a.is_organizer = 1
		WHERE b.host_id = ? AND b.status != 'cancelled' AND b.end_at > ?
		ORDER BY b.start_at ASC`, hostID, now)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list upcoming bookings: query", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	type row struct {
		ID            string `json:"id"`
		StartAt       string `json:"start_at"`
		EndAt         string `json:"end_at"`
		EventTypeName string `json:"event_type_name"`
		EventTypeSlug string `json:"event_type_slug"`
		AttendeeName  string `json:"attendee_name"`
		AttendeeEmail string `json:"attendee_email"`
	}
	out := []row{}
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.ID, &x.StartAt, &x.EndAt, &x.EventTypeName, &x.EventTypeSlug,
			&x.AttendeeName, &x.AttendeeEmail); err != nil {
			continue
		}
		out = append(out, x)
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

// ReassignBooking handles POST /v1/bookings/{id}/reassign (admin).
// Body: {"host_id":"<userId>"}. Moves the booking to another active host,
// checking the new host is free at that time, then (async) moves the Google
// Calendar event and notifies the attendee and the new host.
func (h *Handler) ReassignBooking(w http.ResponseWriter, r *http.Request) {
	actor, ok := userFromContext(r.Context())
	if !ok || !actor.IsAdmin {
		h.writeError(w, http.StatusForbidden, "admin access required")
		return
	}
	id := r.PathValue("id")
	r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
	var req struct {
		HostID string `json:"host_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.HostID == "" {
		h.writeError(w, http.StatusBadRequest, "host_id is required")
		return
	}

	// The new host must exist and be active (not archived).
	var dummy int
	err := h.db.QueryRowContext(r.Context(),
		`SELECT 1 FROM users WHERE id = ? AND archived_at IS NULL`, req.HostID).Scan(&dummy)
	if errors.Is(err, sql.ErrNoRows) {
		h.writeError(w, http.StatusBadRequest, "new host not found or archived")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "reassign: validate host", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Capture the old host + calendar event + summary fields before the move.
	var etName, orgName, orgEmail, orgLocale string
	err = h.db.QueryRowContext(r.Context(), `
		SELECT et.name, COALESCE(a.name,''), COALESCE(a.email,''), COALESCE(a.locale,'')
		FROM bookings b
		JOIN event_types et ON et.id = b.event_type_id
		LEFT JOIN booking_attendees a ON a.booking_id = b.id AND a.is_organizer = 1
		WHERE b.id = ?`, id).
		Scan(&etName, &orgName, &orgEmail, &orgLocale)
	if errors.Is(err, sql.ErrNoRows) {
		h.writeError(w, http.StatusNotFound, "booking not found")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "reassign: load booking", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	updated, changed, err := h.bookingSvc.ReassignHostWithChange(r.Context(), id, req.HostID)
	if errors.Is(err, booking.ErrDoubleBooked) {
		h.writeError(w, http.StatusConflict, "the chosen host already has a booking at that time")
		return
	}
	if errors.Is(err, booking.ErrAlreadyCancelled) {
		h.writeError(w, http.StatusConflict, "this booking has been cancelled")
		return
	}
	if errors.Is(err, booking.ErrHostAlreadyAssigned) {
		h.writeError(w, http.StatusConflict, "the chosen host already attends this booking")
		return
	}
	if errors.Is(err, booking.ErrNotFound) {
		h.writeError(w, http.StatusNotFound, "booking not found")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "reassign: update", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.writeJSON(w, http.StatusOK, toBookingJSON(updated))
	if !changed {
		return
	}

	// Side effects: move the calendar event and notify attendee + new host.
	bCopy := *updated
	newHostID := req.HostID
	go func() { // #nosec G118 -- deliberately its own context.Background(); the request context is cancelled the moment this handler returns
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// Move the Google Calendar event: remove from the old host, recreate on
		// the new host, and persist the new event ID (clearing it if recreation
		// produced nothing, e.g. the new host has no destination calendar).
		inviteMode := bCopy.InviteDelivery
		if gc := h.getCal(); gc != nil {
			// The old target was queued in the SAME transaction as the move.
			// This pass is an optimization; a restart resumes durable cleanup.
			if err := gc.CleanupBooking(ctx, bCopy.ID); err != nil {
				h.logger.Error("reassign: durable old event cleanup", "error", err, "booking_id", bCopy.ID)
				h.nudgeCalendarReconcile()
			}
			current, err := h.bookingSvc.Get(ctx, bCopy.ID)
			if err != nil || current.Status != "confirmed" || current.HostID != newHostID {
				return
			}
			bCopy = *current
			hosts, err := h.assignedHosts(ctx, bCopy.ID)
			if err != nil {
				h.nudgeCalendarReconcile()
				return
			}
			data := mailer.BookingData{BookingID: bCopy.ID, LocationValue: bCopy.LocationValue, Locale: i18n.Get(orgLocale)}
			in := bookingConfirmationInput{EventTypeName: etName, OrganizerName: orgName, OrganizerEmail: orgEmail, OrganizerLocale: orgLocale}
			h.createHostEvents(ctx, &bCopy, in, &data, hosts, bCopy.LocationValue, false, "")
		}

		// Notify the attendee (their host changed) and the new host, reusing the
		// confirmation templates. Host details are overridden to the new host.
		d, err := h.loadCancellationData(ctx, &bCopy)
		if err != nil {
			h.logger.Error("reassign: load email data", "error", err, "booking_id", bCopy.ID)
			return
		}
		d.BaseURL = h.publicURL()
		h.applyBranding(ctx, &d)
		if err := h.loadHostIntoData(ctx, newHostID, &d); err != nil {
			h.logger.Error("reassign: load new host", "error", err, "booking_id", bCopy.ID)
		}
		// A Calnode-sent invite is re-issued (same UID, newer SEQUENCE) so the booker's
		// calendar entry follows the change. A calendar-sent one needs nothing here: the
		// new host's calendar invited the booker when the event was recreated above, and a
		// booking with no invite gets none now either (applyInviteDelivery says so).
		switch inviteMode {
		case booking.InviteByCalnode:
			h.applyInviteDelivery(ctx, &d, inviteMode, newHostID)
			d.ICSSequence = int(time.Now().Unix())
		case booking.InviteByNone:
			h.applyInviteDelivery(ctx, &d, inviteMode, newHostID)
		}
		prefs := h.hostPrefsOrDefault(ctx, bCopy.ID, newHostID)
		if prefs.NotifyConfirmation {
			if err := mailer.SendConfirmationToAttendee(ctx, h.mailer, d); err != nil {
				h.logger.Error("reassign: email attendee", "error", err, "booking_id", bCopy.ID)
			}
		}
		if prefs.NotifyHostBooking {
			hd := d
			h.applyHostInvite(ctx, &hd, inviteMode, newHostID)
			hd.ICSSequence = int(time.Now().Unix())
			if err := mailer.SendConfirmationToHost(ctx, h.mailer, hd); err != nil {
				h.logger.Error("reassign: email new host", "error", err, "booking_id", bCopy.ID)
			}
		}

	}()
}
