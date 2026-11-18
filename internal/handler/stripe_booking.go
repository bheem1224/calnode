package handler

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/calnode/calnode/internal/stripe"
)

// checkoutHoldWindow is how long the slot is held while the booker pays. Stripe requires a
// Checkout session to expire between 30 minutes and 24 hours from creation.
const checkoutHoldWindow = 31 * time.Minute

// startBookingCheckout creates a Stripe Checkout session for a held (pending) booking, marks
// the booking pending_payment, and returns the hosted Checkout URL to redirect the booker to.
func (h *Handler) startBookingCheckout(ctx context.Context, sc *stripe.Client, bookingID string, priceCents int, currency, productName, slug, email string) (string, error) {
	if currency == "" {
		currency = "usd"
	}
	base := h.publicURL()
	sess, err := sc.CreateCheckoutSession(ctx, stripe.CheckoutParams{
		AmountCents:   int64(priceCents),
		Currency:      currency,
		ProductName:   productName,
		CustomerEmail: email,
		// Stripe substitutes {CHECKOUT_SESSION_ID}; the page shows a "payment received" banner.
		SuccessURL: base + "/book/" + slug + "?paid=1&session_id={CHECKOUT_SESSION_ID}",
		CancelURL:  base + "/book/" + slug,
		ExpiresAt:  time.Now().Add(checkoutHoldWindow),
		Metadata:   map[string]string{"booking_id": bookingID},
	})
	if err != nil {
		return "", err
	}
	if _, err := h.db.ExecContext(ctx,
		`UPDATE bookings SET payment_status = 'pending', stripe_session_id = ? WHERE id = ?`,
		sess.ID, bookingID); err != nil {
		return "", err
	}
	return sess.URL, nil
}

// StripeWebhook handles POST /v1/stripe/webhook — Stripe's payment notifications. Public, but
// authenticated by the signing secret (no session cookie, so CSRF middleware doesn't apply).
func (h *Handler) StripeWebhook(w http.ResponseWriter, r *http.Request) {
	sc := h.getStripe()
	if sc == nil || !sc.WebhookConfigured() {
		h.writeError(w, http.StatusServiceUnavailable, "payments not configured")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "could not read body")
		return
	}
	ev, err := sc.VerifyWebhook(body, r.Header.Get("Stripe-Signature"), time.Now())
	if err != nil {
		// Bad signature → 400 so Stripe surfaces the failure (and a forged call is rejected).
		h.logger.WarnContext(r.Context(), "stripe webhook: verify failed", "error", err)
		h.writeError(w, http.StatusBadRequest, "signature verification failed")
		return
	}

	switch ev.Type {
	case "checkout.session.completed":
		sess, perr := ev.Session()
		if perr != nil {
			break
		}
		if sess.PaymentStatus == "paid" {
			if id := sess.Metadata["booking_id"]; id != "" {
				if err := h.confirmPaidBooking(context.Background(), id, sess.PaymentIntent, int(sess.AmountTotal), sess.Currency); err != nil {
					h.writeError(w, 500, "payment confirmation could not be persisted; retry delivery")
					return
				}
			}
		}
	case "checkout.session.expired":
		if sess, perr := ev.Session(); perr == nil {
			if id := sess.Metadata["booking_id"]; id != "" {
				h.releaseUnpaidHold(context.Background(), id)
			}
		}
	}
	// Acknowledge so Stripe stops retrying (we've durably acted, or chosen to ignore).
	w.WriteHeader(http.StatusOK)
}

// confirmPaidBooking flips a held booking to paid, records the charged amount, and fires the
// deferred confirmation side-effects. Idempotent: the conditional UPDATE (payment_status=
// 'pending') ensures only the first delivery of a (possibly retried) webhook dispatches.
func (h *Handler) confirmPaidBooking(ctx context.Context, bookingID, paymentIntentID string, amountCents int, currency string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE bookings SET payment_status='paid',stripe_payment_intent_id=?,amount_paid_cents=?,amount_paid_currency=? WHERE id=? AND payment_status='pending'`, paymentIntentID, amountCents, currency, bookingID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO jobs(id,type,payload,run_at,max_attempts)SELECT ?,'booking.confirm',json_object('booking_id',id),?,8 FROM bookings WHERE id=? AND pending_approval=0 AND status='confirmed'`, "booking.confirm:"+bookingID, time.Now().UTC().Format(time.RFC3339Nano), bookingID); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	// The job now survives a crash even if this optional immediate nudge never runs.
	if b, err := h.bookingSvc.Get(ctx, bookingID); err == nil {
		h.queueBookingConfirmation(b, bookingConfirmationInput{})
	}
	return nil
}

// releaseUnpaidHold cancels a still-pending booking whose Checkout session expired, freeing
// the slot. No-op if the booking already paid or was otherwise resolved.
func (h *Handler) releaseUnpaidHold(ctx context.Context, bookingID string) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	res, err := h.db.ExecContext(ctx,
		`UPDATE bookings SET status = 'cancelled', cancellation_reason = 'payment not completed'
		 WHERE id = ? AND status = 'confirmed' AND payment_status = 'pending'`, bookingID)
	if err != nil {
		h.logger.ErrorContext(ctx, "stripe: release unpaid hold", "error", err, "booking_id", bookingID)
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		h.logger.InfoContext(ctx, "stripe: released unpaid hold", "booking_id", bookingID)
	}
}

// refundBookingPayment issues a Stripe refund when a PAID booking is cancelled.
// Two concurrent cancels must not double-refund, and a Stripe failure must not
// silently keep the money: the row is claimed first with a conditional UPDATE
// (only one claimant wins), the Stripe call carries an idempotency key so a
// retried request returns the original refund, and a failed Stripe call reverts
// the claim so a later cancel retries instead of losing the refund.
func (h *Handler) refundBookingPayment(ctx context.Context, bookingID string) {
	sc := h.getStripe()
	if sc == nil {
		return
	}
	var intentID string
	if err := h.db.QueryRowContext(ctx,
		`SELECT COALESCE(stripe_payment_intent_id, '') FROM bookings WHERE id = ?`,
		bookingID).Scan(&intentID); err != nil {
		return
	}
	if intentID == "" {
		return
	}
	// Claim the refund: only a row still marked paid flips to refunding. A
	// concurrent cancel hitting the same row updates zero rows and stops.
	claimed, err := h.db.ExecContext(ctx,
		`UPDATE bookings SET payment_status = 'refunding'
		 WHERE id = ? AND payment_status = 'paid'`, bookingID)
	if err != nil {
		h.logger.ErrorContext(ctx, "stripe: claim refund", "error", err, "booking_id", bookingID)
		return
	}
	if n, _ := claimed.RowsAffected(); n == 0 {
		return // already refunded/refunding, or never paid
	}
	if err := sc.Refund(ctx, intentID, "refund:"+bookingID); err != nil {
		h.logger.ErrorContext(ctx, "stripe: refund on cancel", "error", err, "booking_id", bookingID)
		// Revert the claim so a later cancel retries; the money was not moved.
		// If the revert itself fails, the row stays 'refunding' — still safe:
		// no other cancel will claim it, and the error is logged above.
		_, _ = h.db.ExecContext(ctx,
			`UPDATE bookings SET payment_status = 'paid'
			 WHERE id = ? AND payment_status = 'refunding'`, bookingID)
		return
	}
	if _, err := h.db.ExecContext(ctx,
		`UPDATE bookings SET payment_status = 'refunded'
		 WHERE id = ? AND payment_status = 'refunding'`, bookingID); err != nil {
		h.logger.ErrorContext(ctx, "stripe: mark refunded", "error", err, "booking_id", bookingID)
	}
}
