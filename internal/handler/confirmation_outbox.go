package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/calnode/calnode/internal/booking"
	"time"
)

// queueBookingConfirmation is an immediate nudge of an already durable job.
// Service.Create/Approve insert the job in their booking transaction. Paid bookings
// enter here only after payment; repeated nudges cannot duplicate the job.
func (h *Handler) queueBookingConfirmation(b *booking.Booking, _ bookingConfirmationInput) {
	if b.PendingApproval || b.PaymentStatus == "pending" {
		return
	}
	ctx := context.Background()
	id := "booking.confirm:" + b.ID
	_, err := h.db.ExecContext(ctx, `INSERT OR IGNORE INTO jobs(id,type,payload,run_at,max_attempts)VALUES(?,'booking.confirm',json_object('booking_id',?),?,8)`, id, b.ID, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		h.logger.Error("queue confirmation", "booking_id", b.ID, "error", err)
		return
	}
	go func() { // #nosec G118 -- durable job persists before this optional latency optimization
		result, e := h.db.ExecContext(ctx, `UPDATE jobs SET status='running',attempts=attempts+1,locked_until=? WHERE id=? AND status='pending' AND attempts<max_attempts AND run_at<=?`, time.Now().UTC().Add(5*time.Minute).Format(time.RFC3339), id, time.Now().UTC().Format(time.RFC3339Nano))
		if e != nil {
			return
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return
		}
		raw, _ := json.Marshal(map[string]string{"booking_id": b.ID})
		e = h.JobBookingConfirm(ctx, string(raw))
		if e != nil {
			_, _ = h.db.ExecContext(ctx, `UPDATE jobs SET status='pending',run_at=?,last_error=?,locked_until=NULL WHERE id=? AND status='running'`, time.Now().UTC().Add(time.Minute).Format(time.RFC3339), e.Error(), id)
		} else {
			_, _ = h.db.ExecContext(ctx, `UPDATE jobs SET status='done',finished_at=?,locked_until=NULL WHERE id=? AND status='running'`, time.Now().UTC().Format(time.RFC3339), id)
		}
	}()
}
func (h *Handler) JobBookingConfirm(ctx context.Context, payload string) error {
	var p struct {
		BookingID string `json:"booking_id"`
	}
	if json.Unmarshal([]byte(payload), &p) != nil || p.BookingID == "" {
		return fmt.Errorf("invalid confirmation job")
	}
	b, err := h.bookingSvc.Get(ctx, p.BookingID)
	if err != nil {
		return err
	}
	if b.Status != "confirmed" {
		return nil
	}
	if b.PendingApproval || b.PaymentStatus == "pending" {
		return fmt.Errorf("awaiting booking approval or payment")
	}
	var in bookingConfirmationInput
	if err = h.db.QueryRowContext(ctx, `SELECT et.name,et.slug,COALESCE(NULLIF(b.location_type,''),et.location_type),o.name,o.email,o.iana_timezone,COALESCE(o.locale,'en') FROM bookings b JOIN event_types et ON et.id=b.event_type_id JOIN booking_attendees o ON o.booking_id=b.id AND o.is_organizer=1 WHERE b.id=?`, b.ID).Scan(&in.EventTypeName, &in.EventTypeSlug, &in.LocationType, &in.OrganizerName, &in.OrganizerEmail, &in.OrganizerTimezone, &in.OrganizerLocale); err != nil {
		return err
	}
	_ = h.db.QueryRowContext(ctx, `SELECT token FROM single_use_tokens WHERE booking_id=?`, b.ID).Scan(&in.SingleUseToken)
	h.dispatchBookingConfirmation(b, in)
	var failed bool
	if err = h.db.QueryRowContext(ctx, `SELECT confirm_failed FROM bookings WHERE id=?`, b.ID).Scan(&failed); err != nil {
		return err
	}
	if failed {
		return fmt.Errorf("confirmation delivery failed")
	}
	return nil
}
func (h *Handler) validateApprovalTime(ctx context.Context, b *booking.Booking) error {
	if b.StartAt.Before(bookingNow().UTC()) {
		return errSlotUnavailable
	}
	var et bookableEventType
	if err := h.db.QueryRowContext(ctx, `SELECT buffer_before_minutes,buffer_after_minutes FROM event_types WHERE id=?`, b.EventTypeID).Scan(&et.BufferBeforeMinutes, &et.BufferAfterMinutes); err != nil {
		return err
	}
	rows, err := h.db.QueryContext(ctx, `SELECT user_id FROM booking_hosts WHERE booking_id=?`, b.ID)
	if err != nil {
		return err
	}
	var hosts []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		hosts = append(hosts, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(hosts) == 0 {
		hosts = []string{b.HostID}
	}
	for _, id := range hosts {
		available, e := h.hostAvailableAt(ctx, id, b.EventTypeID, b.StartAt, b.EndAt)
		if e != nil {
			return e
		}
		if !available {
			return errSlotUnavailable
		}
	}
	_, _, err = h.calendarFreeHosts(ctx, &et, hosts, nil, nil, b.StartAt, b.EndAt)
	return err
}
