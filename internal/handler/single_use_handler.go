package handler

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"html"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/uid"
)

const defaultSingleUseExpiryHours = 168

type singleUseLinkJSON struct {
	ID              string              `json:"id"`
	Token           string              `json:"token"`
	URL             string              `json:"url"`
	EventTypeID     string              `json:"event_type_id"`
	CreatedByUserID string              `json:"created_by_user_id"`
	ExpiresAt       *string             `json:"expires_at"`
	UsedAt          *string             `json:"used_at"`
	BookingID       *string             `json:"booking_id"`
	CreatedAt       string              `json:"created_at"`
	CustomSlots     []booking.AdHocSlot `json:"custom_slots,omitempty"`
}

func (h *Handler) singleUseURL(token string) string {
	base := strings.TrimRight(h.publicURL(), "/")
	if base == "" {
		return "/t/" + token
	}
	return base + "/t/" + token
}

func (h *Handler) resolveEventTypeForSingleUse(ctx context.Context, idOrSlug string, user AuthUser) (etID, etSlug string, err error) {
	isAdmin := 0
	if user.IsAdmin {
		isAdmin = 1
	}
	err = h.db.QueryRowContext(ctx, `
		SELECT id, slug
		FROM event_types
		WHERE (id = ? OR slug = ?)
		  AND (? = 1 OR user_id = ? OR id IN (SELECT event_type_id FROM event_type_hosts WHERE user_id = ?))`,
		idOrSlug, idOrSlug, isAdmin, user.ID, user.ID).Scan(&etID, &etSlug)
	return etID, etSlug, err
}

func generateSingleUseToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// CreateSingleUseLink handles POST /v1/event-types/{id}/single-use-links.
func (h *Handler) CreateSingleUseLink(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	idOrSlug := r.PathValue("id")
	etID, _, err := h.resolveEventTypeForSingleUse(r.Context(), idOrSlug, user)
	if errors.Is(err, sql.ErrNoRows) {
		h.writeError(w, http.StatusNotFound, "event type not found")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "create single-use link: lookup event type", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	var req struct {
		ExpiresInHours   *int            `json:"expires_in_hours"`
		ExpiresInSeconds *int            `json:"expires_in_seconds"`
		CustomSlots      json.RawMessage `json:"custom_slots"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	var customSlotsJSON any
	var customSlots []booking.AdHocSlot
	if req.CustomSlots != nil {
		if err := json.Unmarshal(req.CustomSlots, &customSlots); err != nil || len(customSlots) == 0 || len(customSlots) > 20 {
			h.writeError(w, http.StatusBadRequest, "custom_slots must contain 1 to 20 slots")
			return
		}
		for i, slot := range customSlots {
			for _, previous := range customSlots[:i] {
				delta := previous.StartAt.Sub(slot.StartAt)
				if delta >= -2*time.Second && delta <= 2*time.Second {
					h.writeError(w, http.StatusBadRequest, "custom slot starts must be distinct")
					return
				}
			}
			if slot.StartAt.IsZero() || !slot.EndAt.After(slot.StartAt) || !slot.StartAt.After(time.Now()) {
				h.writeError(w, http.StatusBadRequest, "custom slots must be future intervals with end after start")
				return
			}
		}
		data, err := json.Marshal(customSlots)
		if err != nil {
			h.writeError(w, http.StatusBadRequest, "invalid custom slots")
			return
		}
		customSlotsJSON = string(data)
	}

	expHours := defaultSingleUseExpiryHours
	if req.ExpiresInSeconds != nil && req.ExpiresInHours != nil {
		h.writeError(w, http.StatusBadRequest, "use expires_in_hours or expires_in_seconds, not both")
		return
	}
	if req.ExpiresInSeconds != nil && (*req.ExpiresInSeconds < 1 || *req.ExpiresInSeconds > 604800) {
		h.writeError(w, http.StatusBadRequest, "expires_in_seconds must be between 1 and 604800")
		return
	}
	if req.ExpiresInHours != nil {
		if *req.ExpiresInHours < 0 {
			h.writeError(w, http.StatusBadRequest, "expires_in_hours cannot be negative")
			return
		}
		if int64(*req.ExpiresInHours) > int64(time.Duration(1<<63-1)/time.Hour) {
			h.writeError(w, http.StatusBadRequest, "expires_in_hours is too large")
			return
		}
		expHours = *req.ExpiresInHours
	}

	token, err := generateSingleUseToken()
	if err != nil {
		h.logger.ErrorContext(r.Context(), "create single-use link: generate token", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	id := uid.New()
	nowT := time.Now().UTC()
	nowStr := nowT.Format(time.RFC3339Nano)
	var expiresAtSQL sql.NullString
	var expiresAtPtr *string
	expiry := time.Duration(expHours) * time.Hour
	if req.ExpiresInSeconds != nil {
		expiry = time.Duration(*req.ExpiresInSeconds) * time.Second
	}
	if expiry > 0 {
		expStr := nowT.Add(expiry).Format(time.RFC3339Nano)
		expiresAtSQL = sql.NullString{String: expStr, Valid: true}
		expiresAtPtr = &expStr
	}

	if _, err := h.db.ExecContext(r.Context(), `
		INSERT INTO single_use_tokens
		  (id, token, event_type_id, created_by_user_id, expires_at, created_at, custom_slots_json)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, token, etID, user.ID, expiresAtSQL, nowStr, customSlotsJSON); err != nil {
		h.logger.ErrorContext(r.Context(), "create single-use link: insert", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.writeJSON(w, http.StatusCreated, singleUseLinkJSON{
		ID:              id,
		Token:           token,
		URL:             h.singleUseURL(token),
		EventTypeID:     etID,
		CreatedByUserID: user.ID,
		ExpiresAt:       expiresAtPtr,
		CreatedAt:       nowStr,
		CustomSlots: func() []booking.AdHocSlot {
			if req.CustomSlots != nil {
				return customSlots
			}
			return nil
		}(),
	})
}

// ListSingleUseLinks handles GET /v1/event-types/{id}/single-use-links.
func (h *Handler) ListSingleUseLinks(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	idOrSlug := r.PathValue("id")
	etID, _, err := h.resolveEventTypeForSingleUse(r.Context(), idOrSlug, user)
	if errors.Is(err, sql.ErrNoRows) {
		h.writeError(w, http.StatusNotFound, "event type not found")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list single-use links: lookup event type", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	rows, err := h.db.QueryContext(r.Context(), `
		SELECT id, token, event_type_id, created_by_user_id,
		       expires_at, used_at, booking_id, created_at, custom_slots_json
		FROM single_use_tokens
		WHERE event_type_id = ?
		ORDER BY created_at DESC`, etID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list single-use links: query", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	links := []singleUseLinkJSON{}
	for rows.Next() {
		var item singleUseLinkJSON
		var expiresAt, usedAt, bookingID, customSlots sql.NullString
		if err := rows.Scan(
			&item.ID, &item.Token, &item.EventTypeID, &item.CreatedByUserID,
			&expiresAt, &usedAt, &bookingID, &item.CreatedAt, &customSlots,
		); err != nil {
			h.logger.ErrorContext(r.Context(), "list single-use links: scan", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if customSlots.Valid {
			_ = json.Unmarshal([]byte(customSlots.String), &item.CustomSlots)
		}
		item.URL = h.singleUseURL(item.Token)
		if expiresAt.Valid {
			v := expiresAt.String
			item.ExpiresAt = &v
		}
		if usedAt.Valid {
			v := usedAt.String
			item.UsedAt = &v
		}
		if bookingID.Valid {
			v := bookingID.String
			item.BookingID = &v
		}
		links = append(links, item)
	}
	if err := rows.Err(); err != nil {
		h.logger.ErrorContext(r.Context(), "list single-use links: rows", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]any{"single_use_links": links})
}

// RevokeSingleUseLink handles DELETE /v1/single-use-links/{token}.
func (h *Handler) RevokeSingleUseLink(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	token := strings.TrimSpace(r.PathValue("token"))
	if token == "" {
		h.writeError(w, http.StatusNotFound, "single-use link not found")
		return
	}

	isAdmin := 0
	if user.IsAdmin {
		isAdmin = 1
	}

	var eventTypeID string
	var usedAt sql.NullString
	err := h.db.QueryRowContext(r.Context(), `
		SELECT s.event_type_id, s.used_at
		FROM single_use_tokens s
		JOIN event_types et ON et.id = s.event_type_id
		WHERE s.token = ?
		  AND (? = 1 OR et.user_id = ? OR s.created_by_user_id = ?
		       OR et.id IN (SELECT event_type_id FROM event_type_hosts WHERE user_id = ?))`,
		token, isAdmin, user.ID, user.ID, user.ID).Scan(&eventTypeID, &usedAt)
	if errors.Is(err, sql.ErrNoRows) {
		h.writeError(w, http.StatusNotFound, "single-use link not found")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "revoke single-use link: lookup", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if usedAt.Valid {
		h.writeError(w, http.StatusConflict, "single-use link has already been used")
		return
	}

	res, err := h.db.ExecContext(r.Context(),
		`DELETE FROM single_use_tokens WHERE token = ? AND used_at IS NULL`, token)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "revoke single-use link: delete", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		h.writeError(w, http.StatusConflict, "single-use link has already been used")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// SingleUseBookPage handles GET /t/{token} by validating the ephemeral link and
// rendering the event type booking page prefilled for that token.
func (h *Handler) SingleUseBookPage(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.PathValue("token"))
	if token == "" || !h.dynamicLinkAllowed(r.Context(), token) {
		writeSingleUseGonePage(w)
		return
	}

	var (
		slug      string
		isActive  int
		expiresAt sql.NullString
		usedAt    sql.NullString
	)
	err := h.db.QueryRowContext(r.Context(), `
		SELECT et.slug, et.is_active,
		       s.expires_at, s.used_at
		FROM single_use_tokens s
		JOIN event_types et ON et.id = s.event_type_id
		WHERE s.token = ?`, token).
		Scan(&slug, &isActive, &expiresAt, &usedAt)
	if errors.Is(err, sql.ErrNoRows) {
		writeSingleUseGonePage(w)
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "single-use book page: lookup", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if isActive == 0 || usedAt.Valid {
		writeSingleUseGonePage(w)
		return
	}
	if expiresAt.Valid && expiresAt.String != "" {
		expT, parseErr := time.Parse(time.RFC3339Nano, expiresAt.String)
		if parseErr != nil {
			expT, parseErr = time.Parse(time.RFC3339, expiresAt.String)
		}
		if parseErr != nil || !expT.After(time.Now().UTC()) {
			writeSingleUseGonePage(w)
			return
		}
	}

	h.renderBookPage(w, r, slug, true, bookPrefill{
		SingleUseToken: token,
	})
}

func writeSingleUseGonePage(w http.ResponseWriter) {
	const msg = "This booking link has expired or has already been used"
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusGone)
	_, _ = io.WriteString(w, `<!DOCTYPE html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Link Unavailable</title><script>(function(){try{var m=localStorage.getItem('fp-theme')||'system';var d=m==='dark'||(m!=='light'&&window.matchMedia&&window.matchMedia('(prefers-color-scheme: dark)').matches);document.documentElement.classList.toggle('dark',d);document.documentElement.style.colorScheme=d?'dark':'light';}catch(e){}})();</script><style>:root{--bg:#f8fafc;--fg:#0f172a;--card:#ffffff;--border:#e2e8f0;--muted:#475569;--pill-bg:#ffffff;--pill-active:#0f172a;--pill-active-fg:#ffffff}html.dark{--bg:#09090b;--fg:#f4f4f5;--card:#18181b;--border:#27272a;--muted:#a1a1aa;--pill-bg:#18181b;--pill-active:#f4f4f5;--pill-active-fg:#09090b}body{font-family:system-ui,-apple-system,sans-serif;background:var(--bg);color:var(--fg);display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0;padding:24px;transition:background-color .18s ease,color .18s ease}.box{max-width:440px;background:var(--card);border:1px solid var(--border);border-radius:14px;padding:32px;text-align:center;box-shadow:0 4px 20px rgba(0,0,0,.08)}h1{font-size:20px;margin:0 0 12px;letter-spacing:-0.01em}p{font-size:15px;color:var(--muted);margin:0;line-height:1.55}.sw{position:fixed;top:14px;right:14px;display:inline-flex;gap:2px;padding:3px;border-radius:8px;border:1px solid var(--border);background:var(--pill-bg)}.sw button{border:0;background:transparent;color:var(--muted);font:inherit;font-size:12px;font-weight:500;padding:4px 9px;border-radius:6px;cursor:pointer}.sw button.active{background:var(--pill-active);color:var(--pill-active-fg)}@media(prefers-reduced-motion:reduce){*{transition:none!important}}</style></head><body><div class="sw" role="radiogroup" aria-label="Theme"><button type="button" data-t="light">Light</button><button type="button" data-t="dark">Dark</button><button type="button" data-t="system">System</button></div><main class="box"><h1>Booking Link Unavailable</h1><p>`+html.EscapeString(msg)+`</p></main><script>(function(){var K='fp-theme',mq=window.matchMedia?window.matchMedia('(prefers-color-scheme: dark)'):null;function gm(){try{var v=localStorage.getItem(K);return(v==='light'||v==='dark'||v==='system')?v:'system'}catch(e){return'system'}}function ap(m){var d=m==='dark'||(m!=='light'&&!!(mq&&mq.matches));document.documentElement.classList.toggle('dark',d);document.documentElement.style.colorScheme=d?'dark':'light';var bs=document.querySelectorAll('.sw [data-t]');for(var i=0;i<bs.length;i++){var a=bs[i].getAttribute('data-t')===m;bs[i].classList.toggle('active',a);bs[i].setAttribute('aria-checked',a?'true':'false');}}var bs=document.querySelectorAll('.sw [data-t]');for(var i=0;i<bs.length;i++){bs[i].addEventListener('click',function(){var n=this.getAttribute('data-t')||'system';try{localStorage.setItem(K,n)}catch(e){}ap(n);});}if(mq&&mq.addEventListener)mq.addEventListener('change',function(){ap(gm());});ap(gm());})();</script></body></html>`)
}

func (h *Handler) adHocSlots(ctx context.Context, token, eventID string) ([]booking.AdHocSlot, error) {
	if strings.TrimSpace(token) == "" {
		return nil, nil
	}
	if !h.dynamicLinkAllowed(ctx, token) {
		return nil, booking.ErrSingleUseTokenGone
	}
	var raw sql.NullString
	err := h.db.QueryRowContext(ctx, `SELECT custom_slots_json FROM single_use_tokens WHERE token = ? AND event_type_id = ? AND used_at IS NULL AND (expires_at IS NULL OR datetime(expires_at) > datetime(?))`, token, eventID, time.Now().UTC().Format(time.RFC3339Nano)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, booking.ErrSingleUseTokenGone
	}
	if err != nil {
		return nil, err
	}
	if !raw.Valid || raw.String == "" {
		return nil, nil
	}
	var offered []booking.AdHocSlot
	if err := json.Unmarshal([]byte(raw.String), &offered); err != nil || len(offered) == 0 {
		return nil, booking.ErrAdHocSlotMismatch
	}
	return offered, nil
}

// resolveActiveSingleUseSlug validates an unused, unexpired single-use token and
// returns its underlying event_types.slug without ever exposing that slug to the client.
func (h *Handler) resolveActiveSingleUseSlug(ctx context.Context, token string) (string, bool) {
	token = strings.TrimSpace(token)
	if token == "" || !h.dynamicLinkAllowed(ctx, token) {
		return "", false
	}
	var (
		slug      string
		isActive  int
		expiresAt sql.NullString
		usedAt    sql.NullString
	)
	err := h.db.QueryRowContext(ctx, `
		SELECT et.slug, et.is_active, s.expires_at, s.used_at
		FROM single_use_tokens s
		JOIN event_types et ON et.id = s.event_type_id
		WHERE s.token = ?`, token).
		Scan(&slug, &isActive, &expiresAt, &usedAt)
	if err != nil || isActive == 0 || usedAt.Valid {
		return "", false
	}
	if expiresAt.Valid && expiresAt.String != "" {
		expT, parseErr := time.Parse(time.RFC3339Nano, expiresAt.String)
		if parseErr != nil {
			expT, parseErr = time.Parse(time.RFC3339, expiresAt.String)
		}
		if parseErr != nil || !expT.After(time.Now().UTC()) {
			return "", false
		}
	}
	return slug, true
}

// GetSingleUseSlots handles GET /v1/t/{token}/slots without exposing the event slug.
func (h *Handler) GetSingleUseSlots(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.PathValue("token"))
	slug, ok := h.resolveActiveSingleUseSlug(r.Context(), token)
	if !ok {
		h.writeError(w, http.StatusGone, "This booking link has expired or has already been used")
		return
	}
	r2 := r.Clone(r.Context())
	r2.SetPathValue("slug", slug)
	q := r2.URL.Query()
	q.Set("single_use_token", token)
	r2.URL.RawQuery = q.Encode()
	h.GetSlots(w, r2)
}

// CreateSingleUseBooking handles POST /v1/t/{token}/bookings without exposing the event slug.
func (h *Handler) CreateSingleUseBooking(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.PathValue("token"))
	slug, ok := h.resolveActiveSingleUseSlug(r.Context(), token)
	if !ok {
		h.writeError(w, http.StatusGone, "This booking link has expired or has already been used")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "could not read request body")
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil || payload == nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	payload["event_type_slug"] = slug
	payload["single_use_token"] = token
	rewritten, err := json.Marshal(payload)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	r2 := r.Clone(r.Context())
	r2.Body = io.NopCloser(bytes.NewReader(rewritten))
	r2.ContentLength = int64(len(rewritten))
	h.CreateBooking(w, r2)
}

// SendSingleUseVerificationCode handles POST /v1/t/{token}/verify-email/send without exposing the slug.
func (h *Handler) SendSingleUseVerificationCode(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.PathValue("token"))
	slug, ok := h.resolveActiveSingleUseSlug(r.Context(), token)
	if !ok {
		h.writeError(w, http.StatusGone, "This booking link has expired or has already been used")
		return
	}
	r2 := r.Clone(r.Context())
	r2.SetPathValue("slug", slug)
	h.SendVerificationCode(w, r2)
}

// CheckSingleUseVerificationCode handles POST /v1/t/{token}/verify-email/check without exposing the slug.
func (h *Handler) CheckSingleUseVerificationCode(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.PathValue("token"))
	slug, ok := h.resolveActiveSingleUseSlug(r.Context(), token)
	if !ok {
		h.writeError(w, http.StatusGone, "This booking link has expired or has already been used")
		return
	}
	r2 := r.Clone(r.Context())
	r2.SetPathValue("slug", slug)
	h.CheckVerificationCode(w, r2)
}
