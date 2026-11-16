package handler

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/mail"
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
	email := strings.TrimSpace(strings.ToLower(req.Email))
	if email == "" {
		h.writeError(w, http.StatusBadRequest, "email is required")
		return
	}
	if _, err := mail.ParseAddress(email); err != nil {
		h.writeError(w, http.StatusBadRequest, "email must be a valid email address")
		return
	}

	var etName string
	err := h.db.QueryRowContext(r.Context(),
		`SELECT name FROM event_types WHERE slug = ? AND is_active = 1`, slug).Scan(&etName)
	if errors.Is(err, sql.ErrNoRows) {
		h.writeError(w, http.StatusNotFound, "event type not found")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "verify email send: lookup event type", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
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
	email := strings.TrimSpace(strings.ToLower(req.Email))
	code := strings.TrimSpace(req.Code)
	if email == "" || code == "" {
		h.writeError(w, http.StatusBadRequest, "email and code are required")
		return
	}

	now := bookingNow().UTC().Format(time.RFC3339)
	var id string
	err := h.db.QueryRowContext(r.Context(), `
		SELECT id FROM email_verification_codes
		WHERE event_type_slug = ? AND LOWER(email) = LOWER(?) AND code = ? AND expires_at > ?
		ORDER BY created_at DESC LIMIT 1`,
		slug, email, code, now).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		h.writeError(w, http.StatusBadRequest, "invalid or expired verification code")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "verify email check: lookup", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	token := uid.New()
	if _, err := h.db.ExecContext(r.Context(),
		`UPDATE email_verification_codes SET verified = 1, token = ? WHERE id = ?`,
		token, id); err != nil {
		h.logger.ErrorContext(r.Context(), "verify email check: update", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
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
