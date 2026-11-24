package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/access"
	"github.com/calnode/calnode/internal/netutil"
	"github.com/calnode/calnode/internal/webhook"
)

var validWebhookEvents = []string{
	"booking.created", "booking.cancelled", "booking.rescheduled", "booking.rsvp",
	"recording.completed", "transcript.ready", "notes.ready",
	"single_use_link.booked", "single_use_link.expired",
}

func (h *Handler) validateWebhookEventType(ctx context.Context, userID string, isAdmin bool, eventTypeID *string) (bool, error) {
	if eventTypeID == nil || *eventTypeID == "" {
		return true, nil
	}
	if u, ok := userFromContext(ctx); ok && u.Permissions != nil {
		return access.EventAllowed(ctx, h.db, u.ID, u.Permissions[u.Operation], *eventTypeID), nil
	}
	adminInt := 0
	if isAdmin {
		adminInt = 1
	}
	var id string
	err := h.db.QueryRowContext(ctx, `
		SELECT id FROM event_types
		WHERE id = ? AND (? = 1 OR user_id = ? OR id IN (SELECT event_type_id FROM event_type_hosts WHERE user_id = ?))`,
		*eventTypeID, adminInt, userID, userID).Scan(&id)
	if err != nil {
		return false, err
	}
	return true, nil
}

func (h *Handler) RotateWebhookSecret(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	id := r.PathValue("id")
	var resourceOwner string
	if h.db.QueryRowContext(r.Context(), `SELECT user_id FROM webhooks WHERE id=?`, id).Scan(&resourceOwner) != nil {
		h.writeError(w, 404, "webhook not found")
		return
	}
	if !h.canAccessUser(r.Context(), user, "webhooks.rotate", resourceOwner) {
		h.writeError(w, 403, "webhook access denied")
		return
	}

	secret, err := h.webhookSvc.RotateSecret(r.Context(), resourceOwner, id)
	if err != nil {
		h.writeError(w, 404, "webhook not found")
		return
	}
	h.securityAudit(r.Context(), user.ID, "webhook.rotate", id, nil)
	h.writeJSON(w, 200, map[string]string{"secret": secret})
}

func (h *Handler) CreateWebhook(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)

	var req struct {
		URL         string           `json:"url"`
		Events      []string         `json:"events"`
		Fields      []string         `json:"fields"` // optional payload field selection; nil = default set
		EventTypeID *string          `json:"event_type_id"`
		Scopes      *[]webhook.Scope `json:"scopes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.URL == "" {
		h.writeError(w, http.StatusBadRequest, "url is required")
		return
	}
	u, err := url.ParseRequestURI(req.URL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		h.writeError(w, http.StatusBadRequest, "url must be a valid http or https URL")
		return
	}
	if err := validateWebhookURL(r.Context(), u); err != nil {
		h.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.Events) == 0 {
		h.writeError(w, http.StatusBadRequest, "events must not be empty")
		return
	}
	for _, e := range req.Events {
		if !slices.Contains(validWebhookEvents, e) {
			h.writeError(w, http.StatusBadRequest, "unknown event: "+e)
			return
		}
	}
	if !h.validateWebhookDataGrants(w, user, req.Events, req.Fields) {
		return
	}
	if ok, err := h.validateWebhookEventType(r.Context(), user.ID, user.IsAdmin, req.EventTypeID); !ok {
		if err != nil && err.Error() != "sql: no rows in result set" {
			h.logger.ErrorContext(r.Context(), "create webhook: validate event_type_id", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		h.writeError(w, http.StatusNotFound, "event type not found")
		return
	}

	if req.Scopes != nil && req.EventTypeID != nil && *req.EventTypeID != "" {
		h.writeError(w, 400, "use scopes or event_type_id, not both")
		return
	}
	if err := h.validateWebhookScopes(r.Context(), user, req.Scopes, webhookScopeEvents(req.Events, req.Fields)); err != nil {
		h.writeError(w, 403, err.Error())
		return
	}
	wh, secret, err := h.webhookSvc.CreateConfigured(r.Context(), user.ID, req.URL, req.Events, webhook.Options{Fields: req.Fields, EventTypeID: req.EventTypeID, Scopes: req.Scopes, Permissions: user.Permissions})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "create webhook", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.securityAudit(r.Context(), user.ID, "webhook.create", wh.ID, map[string]any{"events": req.Events})

	h.writeJSON(w, http.StatusCreated, map[string]any{
		"id":            wh.ID,
		"url":           wh.URL,
		"events":        wh.Events,
		"fields":        wh.Fields,
		"event_type_id": wh.EventTypeID,
		"scopes":        wh.Scopes,
		"secret":        secret,
		"is_active":     wh.IsActive,
		"created_at":    wh.CreatedAt.UTC().Format(time.RFC3339),
	})
}

// PatchWebhook applies a validated partial update without rotating the signing secret.
func (h *Handler) PatchWebhook(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	id := r.PathValue("id")
	var resourceOwner string
	if h.db.QueryRowContext(r.Context(), `SELECT user_id FROM webhooks WHERE id=?`, id).Scan(&resourceOwner) != nil {
		h.writeError(w, 404, "webhook not found")
		return
	}
	if !h.canAccessUser(r.Context(), user, "webhooks.update", resourceOwner) {
		h.writeError(w, 403, "webhook access denied")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)

	var req struct {
		URL         *string          `json:"url"`
		IsActive    *bool            `json:"is_active"`
		Events      *[]string        `json:"events"`
		Fields      *[]string        `json:"fields"`
		EventTypeID *string          `json:"event_type_id"`
		Scopes      *[]webhook.Scope `json:"scopes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.URL != nil {
		u, err := url.ParseRequestURI(*req.URL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
			h.writeError(w, http.StatusBadRequest, "url must be a valid http or https URL")
			return
		}
		if err := validateWebhookURL(r.Context(), u); err != nil {
			h.writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if req.EventTypeID != nil && *req.EventTypeID != "" {
		if ok, err := h.validateWebhookEventType(r.Context(), user.ID, user.IsAdmin, req.EventTypeID); !ok {
			if err != nil && err.Error() != "sql: no rows in result set" {
				h.logger.ErrorContext(r.Context(), "update webhook: validate event_type_id", "error", err)
				h.writeError(w, http.StatusInternalServerError, "internal error")
				return
			}
			h.writeError(w, http.StatusNotFound, "event type not found")
			return
		}
	}
	if req.Events != nil {
		if len(*req.Events) == 0 {
			h.writeError(w, http.StatusBadRequest, "events must not be empty")
			return
		}
		for _, e := range *req.Events {
			if !slices.Contains(validWebhookEvents, e) {
				h.writeError(w, http.StatusBadRequest, "unknown event: "+e)
				return
			}
		}
	}
	var fields []string
	var events []string
	if req.Fields != nil {
		fields = *req.Fields
	}
	if req.Events != nil {
		events = *req.Events
	}
	if !h.validateWebhookDataGrants(w, user, events, fields) {
		return
	}

	if req.Scopes != nil && req.EventTypeID != nil && *req.EventTypeID != "" {
		h.writeError(w, 400, "use scopes or event_type_id, not both")
		return
	}
	// Validate against the owner's saved ceiling as well as the current editor.
	var raw string
	if h.db.QueryRowContext(r.Context(), `SELECT permissions_json FROM webhooks WHERE id=?`, id).Scan(&raw) != nil {
		h.writeError(w, 500, "could not load webhook permissions")
		return
	}
	current, err := access.Load(r.Context(), h.db, resourceOwner)
	if err != nil {
		h.writeError(w, 403, "webhook owner is unavailable")
		return
	}
	effective := access.Intersect(current, access.Parse(raw))
	owner := AuthUser{ID: resourceOwner, Permissions: effective}

	var existingEvents, existingFields, existingScopes sql.NullString
	if h.db.QueryRowContext(r.Context(), `SELECT events,fields,scopes_json FROM webhooks WHERE id=?`, id).Scan(&existingEvents, &existingFields, &existingScopes) != nil {
		h.writeError(w, 500, "could not load subscription")
		return
	}
	if req.Events == nil {
		_ = json.Unmarshal([]byte(existingEvents.String), &events)
	}
	if req.Fields == nil {
		_ = json.Unmarshal([]byte(existingFields.String), &fields)
	}
	validatedScopes := req.Scopes
	if validatedScopes == nil && req.EventTypeID == nil && existingScopes.Valid {
		var saved []webhook.Scope
		if json.Unmarshal([]byte(existingScopes.String), &saved) != nil {
			h.writeError(w, 500, "invalid saved scopes")
			return
		}
		validatedScopes = &saved
	}
	if !h.validateWebhookDataGrants(w, user, events, fields) || !h.validateWebhookDataGrants(w, owner, events, fields) {
		return
	}
	for _, principal := range []AuthUser{user, owner} {
		if err = h.validateWebhookScopes(r.Context(), principal, validatedScopes, webhookScopeEvents(events, fields)); err != nil {
			h.writeError(w, 403, err.Error())
			return
		}
	}

	if err := h.webhookSvc.UpdateConfig(r.Context(), resourceOwner, id, webhook.ConfigPatch{URL: req.URL, IsActive: req.IsActive, Events: req.Events, Fields: req.Fields, EventTypeID: req.EventTypeID, Scopes: req.Scopes}); err != nil {
		if errors.Is(err, webhook.ErrNotFound) {
			h.writeError(w, 404, "webhook not found")
		} else {
			h.writeError(w, 500, "could not update webhook")
		}
		return
	}

	h.securityAudit(r.Context(), user.ID, "webhook.update", id, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) ListWebhooks(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())

	target := r.URL.Query().Get("user_id")
	if target == "" {
		target = user.ID
	}
	if !h.canAccessUser(r.Context(), user, "webhooks.read", target) {
		h.writeError(w, 403, "webhook access denied")
		return
	}
	webhooks, err := h.webhookSvc.List(r.Context(), target)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list webhooks", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	items := make([]map[string]any, len(webhooks))
	for i, wh := range webhooks {
		items[i] = map[string]any{
			"id":            wh.ID,
			"url":           wh.URL,
			"events":        wh.Events,
			"fields":        wh.Fields,
			"event_type_id": wh.EventTypeID,
			"scopes":        wh.Scopes,
			"is_active":     wh.IsActive,
			"created_at":    wh.CreatedAt.UTC().Format(time.RFC3339),
		}
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) DeleteWebhook(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	id := r.PathValue("id")
	var resourceOwner string
	if h.db.QueryRowContext(r.Context(), `SELECT user_id FROM webhooks WHERE id=?`, id).Scan(&resourceOwner) != nil {
		h.writeError(w, 404, "webhook not found")
		return
	}
	if !h.canAccessUser(r.Context(), user, "webhooks.delete", resourceOwner) {
		h.writeError(w, 403, "webhook access denied")
		return
	}

	if err := h.webhookSvc.Delete(r.Context(), resourceOwner, id); err != nil {
		if errors.Is(err, webhook.ErrNotFound) {
			h.writeError(w, http.StatusNotFound, "webhook not found")
			return
		}
		h.logger.ErrorContext(r.Context(), "delete webhook", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.securityAudit(r.Context(), user.ID, "webhook.delete", id, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) ListWebhookDeliveries(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	webhookID := r.PathValue("id")
	var resourceOwner string
	if h.db.QueryRowContext(r.Context(), `SELECT user_id FROM webhooks WHERE id=?`, webhookID).Scan(&resourceOwner) != nil {
		h.writeError(w, 404, "webhook not found")
		return
	}
	if !h.canAccessUser(r.Context(), user, "webhooks.deliveries", resourceOwner) {
		h.writeError(w, 403, "webhook access denied")
		return
	}

	deliveries, err := h.webhookSvc.ListDeliveries(r.Context(), resourceOwner, webhookID)
	if err != nil {
		if errors.Is(err, webhook.ErrNotFound) {
			h.writeError(w, http.StatusNotFound, "webhook not found")
			return
		}
		h.logger.ErrorContext(r.Context(), "list deliveries", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	items := make([]map[string]any, len(deliveries))
	for i, d := range deliveries {
		item := map[string]any{
			"id":              d.ID,
			"webhook_id":      d.WebhookID,
			"event":           d.Event,
			"status":          d.Status,
			"attempt_count":   d.AttemptCount,
			"last_error":      d.LastError,
			"next_attempt_at": d.NextAttemptAt,
		}
		if d.BookingID != "" {
			item["booking_id"] = d.BookingID
		}
		if d.ResponseStatus != nil {
			item["response_status"] = *d.ResponseStatus
		}
		if d.LastAttemptedAt != nil {
			item["last_attempted_at"] = *d.LastAttemptedAt
		}
		items[i] = item
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// validateWebhookURL resolves the URL host and rejects any address in a
// loopback, link-local, or private range to prevent SSRF.
// validateWebhookURL rejects a webhook URL whose host resolves (now) to a private/
// loopback address — the same SSRF check the worker re-applies at actual delivery
// time (netutil.ResolveSafe), since DNS can change between saving a URL and
// delivering to it.
func validateWebhookURL(ctx context.Context, u *url.URL) error {
	if u.User != nil || u.Fragment != "" || u.Hostname() == "" {
		return fmt.Errorf("webhook URL must have a hostname and must not contain credentials or a fragment")
	}
	if _, err := netutil.ResolveSafe(ctx, u.Hostname()); err != nil {
		return fmt.Errorf("webhook URL must not resolve to a private or loopback address: %w", err)
	}
	return nil
}

func (h *Handler) validateWebhookDataGrants(w http.ResponseWriter, u AuthUser, events, fields []string) bool {
	if u.Permissions == nil {
		return true
	} // direct legacy internal callers
	for _, event := range events {
		permission := "bookings.read"
		switch {
		case strings.HasPrefix(event, "recording."):
			permission = "recordings.read"
		case strings.HasPrefix(event, "transcript."):
			permission = "transcripts.read"
		case strings.HasPrefix(event, "notes."):
			permission = "notes.read"
		case strings.HasPrefix(event, "single_use_link."):
			permission = "event_types.links"
		}
		if !u.Permissions.Has(permission) && !(permission == "event_types.links" && u.Permissions.Has("team_event_types.links")) {
			h.writeError(w, 403, "permission required for webhook data: "+permission)
			return false
		}
	}
	if slices.Contains(fields, "answers") && !u.Permissions.Has("bookings.answers") {
		h.writeError(w, 403, "permission required for webhook answers: bookings.answers")
		return false
	}
	return true
}

func (h *Handler) RetryWebhookDelivery(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	id := r.PathValue("id")
	deliveryID := r.PathValue("deliveryId")
	var owner, bookingID, eventID, event, resource string
	err := h.db.QueryRowContext(r.Context(), `SELECT wh.user_id,COALESCE(d.booking_id,''),COALESCE(d.event_type_id,''),d.event,COALESCE(d.resource_id,'') FROM webhook_deliveries d JOIN webhooks wh ON wh.id=d.webhook_id WHERE wh.id=? AND d.id=?`, id, deliveryID).Scan(&owner, &bookingID, &eventID, &event, &resource)
	if err != nil {
		h.writeError(w, 404, "webhook delivery not found")
		return
	}
	if !user.Permissions.Has("webhooks.deliveries") || !h.canAccessUser(r.Context(), user, "webhooks.update", owner) || !h.canAccessUser(r.Context(), user, "webhooks.deliveries", owner) {
		h.writeError(w, 403, "webhook access denied")
		return
	}
	if !h.webhookSvc.DeliveryAuthorized(r.Context(), id, bookingID, eventID, event, resource) {
		h.writeError(w, 403, "delivery is no longer authorized")
		return
	}
	if err = h.webhookSvc.RetryDelivery(r.Context(), owner, id, deliveryID); err != nil {
		h.writeError(w, 409, err.Error())
		return
	}
	h.securityAudit(r.Context(), user.ID, "webhook.retry", deliveryID, nil)
	h.writeJSON(w, 200, map[string]string{"status": "pending"})
}
