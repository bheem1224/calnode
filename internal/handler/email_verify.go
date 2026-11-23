package handler

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/calnode/calnode/internal/abuse"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/mailer"
	"github.com/calnode/calnode/internal/uid"
)

// SendVerificationCode handles POST /v1/book/{slug}/verify-email/send (public).
// Generates a 6-digit OTP for booker email verification and sends it via mailer.
func (h *Handler) SendVerificationCode(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)

	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	email, err := normalizeBookerEmail(req.Email)
	email = strings.ToLower(email)
	if err != nil {
		h.writeError(w, 400, "email must be a valid email address")
		return
	}

	et, err := h.loadBookableEventTypeWithVisibility(r.Context(), slug, r.PathValue("token") != "")
	if errors.Is(err, errEventTypeNotFound) {
		h.writeError(w, http.StatusNotFound, "event type not found")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "verify email send: lookup event type", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	etName := et.Name

	if !h.takeBudget(w, r,
		abuse.Rule{Key: "verify:cooldown:" + email, Limit: 1, Window: time.Minute},
		abuse.Rule{Key: "verify:recipient:" + email, Limit: 5, Window: time.Hour},
		abuse.Rule{Key: "verify:event:" + slug, Limit: abuse.Limit("VERIFICATION_EVENT_HOURLY_LIMIT", 120), Window: time.Hour},
		abuse.Rule{Key: "verify:instance", Limit: abuse.Limit("VERIFICATION_HOURLY_LIMIT", 600), Window: time.Hour}) {
		return
	}

	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		h.logger.ErrorContext(r.Context(), "verify email send: rand", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	code := fmt.Sprintf("%06d", n.Int64())
	expiresAt := bookingNow().UTC().Add(15 * time.Minute).Format(time.RFC3339)

	if _, err := h.db.ExecContext(r.Context(),
		`DELETE FROM email_verification_codes WHERE event_type_slug = ? AND LOWER(email) = LOWER(?)`,
		slug, email); err != nil {
		h.logger.ErrorContext(r.Context(), "verify email send: clear old", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if _, err := h.db.ExecContext(r.Context(), `
		INSERT INTO email_verification_codes (id, event_type_slug, email, code, expires_at)
		VALUES (?, ?, ?, ?, ?)`,
		uid.New(), slug, email, code, expiresAt); err != nil {
		h.logger.ErrorContext(r.Context(), "verify email send: insert", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if err := h.mailer.Send(r.Context(), mailer.Message{
		To:      []string{email},
		Subject: fmt.Sprintf("Verification code for %s: %s", etName, code),
		Text:    fmt.Sprintf("Your verification code for %s is: %s\n\nThis code expires in 15 minutes.", etName, code),
	}); err != nil {
		h.logger.ErrorContext(r.Context(), "verify email send: mailer", "error", err)
		h.writeError(w, http.StatusBadGateway, "failed to send verification email")
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]any{"sent": true})
}

// CheckVerificationCode handles POST /v1/book/{slug}/verify-email/check (public).
// Validates the 6-digit code and returns a single-use verification_token.
func (h *Handler) CheckVerificationCode(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)

	var req struct {
		Email string `json:"email"`
		Code  string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	email, err := normalizeBookerEmail(req.Email)
	email = strings.ToLower(email)
	code := strings.TrimSpace(req.Code)
	if err != nil || len(code) != 6 {
		h.writeError(w, 400, "invalid or expired verification code")
		return
	}
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.writeError(w, 503, "temporarily unavailable")
		return
	}
	defer tx.Rollback()
	now := bookingNow().UTC().Format(time.RFC3339)
	var id, expected string
	// Increment before comparing, inside the transaction, including incorrect guesses.
	err = tx.QueryRowContext(r.Context(), `UPDATE email_verification_codes SET attempts=attempts+1 WHERE id=(SELECT id FROM email_verification_codes WHERE event_type_slug=? AND LOWER(email)=LOWER(?) AND expires_at>? AND verified=0 ORDER BY created_at DESC LIMIT 1) AND attempts<5 RETURNING id,code`, slug, email, now).Scan(&id, &expected)
	if err == sql.ErrNoRows {
		h.writeError(w, 400, "invalid or expired verification code")
		return
	}
	if err != nil {
		h.writeError(w, 503, "temporarily unavailable")
		return
	}
	if subtle.ConstantTimeCompare([]byte(code), []byte(expected)) != 1 {
		if tx.Commit() != nil {
			h.writeError(w, 503, "temporarily unavailable")
			return
		}
		h.writeError(w, 400, "invalid or expired verification code")
		return
	}
	token := uid.New()
	if _, err = tx.ExecContext(r.Context(), `UPDATE email_verification_codes SET verified=1,token=?,code='' WHERE id=?`, token, id); err != nil {
		h.writeError(w, 503, "temporarily unavailable")
		return
	}
	if tx.Commit() != nil {
		h.writeError(w, 503, "temporarily unavailable")
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]any{"verification_token": token})
}

func (h *Handler) consumeEmailVerificationToken(ctx context.Context, slug, email, token string) bool {
	token = strings.TrimSpace(token)
	email = strings.TrimSpace(email)
	if token == "" || email == "" {
		return false
	}
	now := bookingNow().UTC().Format(time.RFC3339)
	res, err := h.db.ExecContext(ctx, `
		DELETE FROM email_verification_codes
		WHERE event_type_slug = ? AND LOWER(email) = LOWER(?) AND token = ? AND verified = 1 AND expires_at > ?`,
		slug, email, token, now)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}
