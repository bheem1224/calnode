package handler

import (
	"context"
	"errors"
	"time"

	"github.com/calnode/calnode/internal/calendar"
	"github.com/calnode/calnode/internal/i18n"
	"github.com/calnode/calnode/internal/mailer"
)

// calReconcileInterval is how often the reconciler sweeps for divergence between
// booking state and Google Calendar. Failed inline ops also nudge it to run sooner.
const calReconcileInterval = 2 * time.Minute

// StartCalendarReconciler launches the calendar reconciler: a pass at startup,
// then on a ticker, plus whenever an inline calendar op fails (nudge). It heals
// divergence between booking state and the calendar — deleting events left behind
// by cancelled bookings, and creating events missing from upcoming confirmed
// bookings (e.g. when the original attempt failed due to a network blip). It
// stops when ctx is cancelled. Safe to call when calendar is unconfigured — each
// pass no-ops until a client is set.
func (h *Handler) StartCalendarReconciler(ctx context.Context) {
	go func() {
		h.reconcileCalendar()
		ticker := time.NewTicker(calReconcileInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				h.reconcileCalendar()
			case <-h.calNudge:
				h.reconcileCalendar()
			}
		}
	}()
}

// nudgeCalendarReconcile asks the reconciler to run soon. Non-blocking and
// coalesced (a pending nudge is enough); safe to call from inline op goroutines.
func (h *Handler) nudgeCalendarReconcile() {
	select {
	case h.calNudge <- struct{}{}:
	default:
	}
}

func (h *Handler) reconcileCalendar() {
	gc := h.getCal()
	zc := h.getZoom()
	if gc == nil && zc == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if gc != nil {
		h.reconcileCancellations(ctx, gc)
		h.reconcileCreations(ctx, gc)
		h.reconcileReschedules(ctx, gc)
	}
	if zc != nil {
		if err := zc.ReconcileCleanup(ctx); err != nil {
			h.logger.Error("reconcile: Zoom lifecycle", "error", err)
		}
	}
}

// reconcileReschedules re-applies the calendar time for booking_hosts rows flagged
// needs_sync — i.e. an inline reschedule move (UpdateEvent) failed and the event is
// stranded at the old time. This is the time-drift counterpart to the presence/
// absence passes: drift can't be inferred from booking state, so it's signalled by
// the flag. Idempotent — re-applying the same time is a no-op on Google.
func (h *Handler) reconcileReschedules(ctx context.Context, gc *calendar.Service) {
	now := time.Now().UTC().Format(time.RFC3339)
	type drift struct {
		bookingID, userID, eventID, calendarID, provider, startStr, endStr string
	}
	// Read fully before acting — the single DB connection can't serve the inner
	// UpdateEvent/UPDATE while a cursor is open (would deadlock). Only upcoming
	// confirmed bookings matter; past ones need no calendar correction.
	var items []drift
	rows, err := h.db.QueryContext(ctx, `
		SELECT bh.booking_id, bh.user_id, bh.external_event_id, COALESCE(bh.external_calendar_id, ''), COALESCE(bh.external_provider, ''), b.start_at, b.end_at
		FROM booking_hosts bh JOIN bookings b ON b.id = bh.booking_id
		WHERE bh.needs_sync = 1 AND b.status = 'confirmed'
		  AND COALESCE(bh.external_event_id, '') != '' AND b.end_at > ?`, now)
	if err != nil {
		h.logger.Error("reconcile: query drifted events", "error", err)
		return
	}
	for rows.Next() {
		var d drift
		if err := rows.Scan(&d.bookingID, &d.userID, &d.eventID, &d.calendarID, &d.provider, &d.startStr, &d.endStr); err == nil {
			items = append(items, d)
		}
	}
	rows.Close() // #nosec G104 -- rows already fully consumed above; nothing actionable on close error

	for _, d := range items {
		start, err1 := time.Parse(time.RFC3339Nano, d.startStr)
		end, err2 := time.Parse(time.RFC3339Nano, d.endStr)
		if err1 != nil || err2 != nil {
			// Corrupt row: the stored times will never parse, so retrying every
			// sweep forever helps nothing. Quarantine it (clear the flag) with a
			// warning rather than spinning on it until the booking ends.
			h.logger.Warn("reconcile: unparseable booking times; clearing needs_sync",
				"booking_id", d.bookingID, "host", d.userID,
				"start", d.startStr, "end", d.endStr)
			if _, err := h.db.ExecContext(ctx,
				`UPDATE booking_hosts SET needs_sync = 0 WHERE booking_id = ? AND user_id = ?`,
				d.bookingID, d.userID); err != nil {
				h.logger.Error("reconcile: clear needs_sync", "error", err, "booking_id", d.bookingID)
			}
			continue
		}
		if err := gc.UpdateEvent(ctx, d.userID, d.calendarID, d.eventID, d.provider, start, end); err != nil {
			if !errors.Is(err, calendar.ErrEventUnreachable) {
				h.logger.Error("reconcile: re-apply event time", "error", err, "booking_id", d.bookingID, "host", d.userID)
				continue // leave the flag set; retry next sweep
			}
			// Nothing was sent, and the next sweep would re-read the same connections and
			// refuse the same way, so clear the flag rather than log this every sweep until
			// the booking ends. The event keeps its old time.
			h.logger.Warn("reconcile: not retrying an event no single account holds; it keeps its old time",
				"error", err, "booking_id", d.bookingID, "host", d.userID)
		}
		h.clearCalendarSync(ctx, d.bookingID, d.userID, start, end)
	}
}

// reconcileCancellations deletes calendar events still attached to cancelled
// bookings (the inline cancel never reached Google). On success the per-host
// event id is cleared so the row drops out of the next sweep.
func (h *Handler) reconcileCancellations(ctx context.Context, gc *calendar.Service) {
	if err := gc.ReconcileCleanup(ctx); err != nil {
		h.logger.Error("reconcile: durable calendar cleanup", "error", err)
	}
}

// reconcileCreations creates calendar events missing from upcoming confirmed
// bookings — hosts who should have an event but don't (the inline create failed).
// Hosts without a destination calendar are skipped so we don't retry forever.
func (h *Handler) reconcileCreations(ctx context.Context, gc *calendar.Service) {
	now := time.Now().UTC()
	type missing struct{ id, name, locationType, locationValue, orgName, orgEmail, orgLocale string }
	rows, err := h.db.QueryContext(ctx, `SELECT DISTINCT b.id,et.name,COALESCE(NULLIF(b.location_type,''),et.location_type),COALESCE(b.location_value,''),COALESCE(o.name,''),COALESCE(o.email,''),COALESCE(o.locale,'en')
 FROM booking_hosts bh JOIN bookings b ON b.id=bh.booking_id JOIN event_types et ON et.id=b.event_type_id LEFT JOIN booking_attendees o ON o.booking_id=b.id AND o.is_organizer=1
 WHERE b.status='confirmed' AND b.pending_approval=0 AND COALESCE(b.payment_status,'') NOT IN('pending','refunded','refunding') AND b.end_at>? AND b.created_at<? AND COALESCE(bh.external_event_id,'')=''`, now.Format(time.RFC3339), now.Add(-5*time.Minute).Format(time.RFC3339))
	if err != nil {
		h.logger.Error("reconcile: query missing events", "error", err)
		return
	}
	var items []missing
	for rows.Next() {
		var m missing
		if rows.Scan(&m.id, &m.name, &m.locationType, &m.locationValue, &m.orgName, &m.orgEmail, &m.orgLocale) == nil {
			items = append(items, m)
		}
	}
	rows.Close()
	for _, m := range items {
		b, err := h.bookingSvc.Get(ctx, m.id)
		if err != nil {
			h.logger.Error("reconcile: load booking", "booking_id", m.id, "error", err)
			continue
		}
		if m.locationType == "zoom" {
			if zc := h.getZoom(); zc != nil {
				connected, err := zc.Connected(ctx, b.HostID)
				var mid string
				if err == nil && connected {
					err = h.db.QueryRowContext(ctx, `SELECT COALESCE(zoom_meeting_id,'') FROM bookings WHERE id=?`, b.ID).Scan(&mid)
				}
				if err != nil || (connected && mid == "") {
					continue
				}
			}
		}
		hosts, err := h.assignedHosts(ctx, m.id)
		if err != nil || len(hosts) == 0 {
			continue
		}
		// The canonical creator skips existing host events. All host metadata remains
		// available for title variables even when only a secondary calendar needs repair.
		in := bookingConfirmationInput{EventTypeName: m.name, LocationType: m.locationType, OrganizerName: m.orgName, OrganizerEmail: m.orgEmail, OrganizerLocale: m.orgLocale}
		data := mailer.BookingData{BookingID: b.ID, LocationValue: m.locationValue, Locale: i18n.Get(m.orgLocale)}
		auto := m.locationValue == "" && h.calendarMeetingAutoGenerate(ctx, b.EventTypeID, primaryHost(hosts).UserID, m.locationType)
		h.createHostEvents(ctx, b, in, &data, hosts, m.locationValue, auto, "")
	}
}
