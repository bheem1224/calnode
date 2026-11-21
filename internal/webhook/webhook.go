package webhook

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/access"
	"github.com/calnode/calnode/internal/uid"
)

var ErrNotFound = errors.New("webhook: not found")

type Webhook struct {
	ID          string
	UserID      string
	URL         string
	Events      []string
	Fields      []string // payload field keys; nil means the default set
	EventTypeID *string  // legacy account/single-event selector; ignored when Scopes is configured
	Scopes      []Scope
	IsActive    bool
	CreatedAt   time.Time
}

// Payload field keys (the JSON keys in a delivery's "data" object).
const (
	FieldID              = "id"
	FieldStatus          = "status"
	FieldStartAt         = "start_at"
	FieldEndAt           = "end_at"
	FieldCreatedAt       = "created_at"
	FieldLocation        = "location_value"
	FieldCancelReason    = "cancellation_reason"
	FieldPreviousStartAt = "previous_start_at"
	FieldPreviousEndAt   = "previous_end_at"
	FieldEventTypeSlug   = "event_type_slug"
	FieldEventTypeName   = "event_type_name"
	FieldHostID          = "host_id"
	FieldHostName        = "host_name"
	FieldHostEmail       = "host_email"
	FieldAttendeeName    = "attendee_name"
	FieldAttendeeEmail   = "attendee_email"
	FieldAttendeeTZ      = "attendee_timezone"
	FieldAnswers         = "answers"
	FieldPaymentStatus   = "payment_status"
	FieldAmountPaid      = "amount_paid_cents"
	FieldCurrency        = "amount_paid_currency"
	FieldRSVPStatus      = "rsvp_status"
	FieldInviteDelivery  = "invite_delivery"
	FieldToken           = "token"
	FieldEventTypeID     = "event_type_id"
	FieldBookingID       = "booking_id"
	FieldExpiresAt       = "expires_at"
	FieldUsedAt          = "used_at"
	FieldSingleUseLinkID = "single_use_link_id"
)

// AllFields is every selectable field, in payload order. Used to validate config
// and (UI) to render the checkbox list.
var AllFields = []string{
	FieldID, FieldStatus, FieldStartAt, FieldEndAt, FieldCreatedAt,
	FieldLocation, FieldCancelReason, FieldPreviousStartAt, FieldPreviousEndAt,
	FieldEventTypeSlug, FieldEventTypeName,
	FieldHostID, FieldHostName, FieldHostEmail,
	FieldAttendeeName, FieldAttendeeEmail, FieldAttendeeTZ, FieldAnswers,
	FieldPaymentStatus, FieldAmountPaid, FieldCurrency, FieldRSVPStatus,
	FieldInviteDelivery, FieldToken, FieldEventTypeID, FieldBookingID, FieldExpiresAt, FieldUsedAt, FieldSingleUseLinkID,
}

// defaultFields reproduces the original payload (no PII, no answers) so a webhook with no
// field config (fields IS NULL) keeps its historical shape. Payment fields are included but
// omitempty, so free bookings are byte-identical to before; only paid bookings gain them.
// FieldInviteDelivery is opt-in for the same reason: every booking has a value, so adding
// it here would change the payload of every unconfigured webhook.
var defaultFields = []string{
	FieldID, FieldEventTypeSlug, FieldHostID, FieldStartAt, FieldEndAt,
	FieldStatus, FieldLocation, FieldCancelReason, FieldCreatedAt,
	FieldPreviousStartAt, FieldPreviousEndAt,
	FieldPaymentStatus, FieldAmountPaid, FieldCurrency, FieldRSVPStatus,
}

var validField = func() map[string]bool {
	m := make(map[string]bool, len(AllFields))
	for _, f := range AllFields {
		m[f] = true
	}
	return m
}()

// ValidFields filters fields to known keys, preserving order. Used to sanitise
// caller-supplied config.
func ValidFields(fields []string) []string {
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if validField[f] {
			out = append(out, f)
		}
	}
	return out
}

type Delivery struct {
	ID              string
	WebhookID       string
	BookingID       string
	Event           string
	Status          string
	ResponseStatus  *int
	AttemptCount    int
	LastError       string
	NextAttemptAt   *string
	LastAttemptedAt *string
}

// BookingAnswerPayload carries one custom question answer inside a webhook payload.
type BookingAnswerPayload struct {
	QuestionID string `json:"question_id"`
	Label      string `json:"label"`
	Value      string `json:"value"`
}

// BookingPayload is the "data" object inside every webhook envelope.
type BookingPayload struct {
	OccurredAt         string                 `json:"occurred_at,omitempty"`
	ID                 string                 `json:"id"`
	EventTypeSlug      string                 `json:"event_type_slug"`
	HostID             string                 `json:"host_id"`
	StartAt            string                 `json:"start_at"`
	EndAt              string                 `json:"end_at"`
	Status             string                 `json:"status"`
	CancellationReason string                 `json:"cancellation_reason,omitempty"`
	LocationValue      string                 `json:"location_value,omitempty"`
	CreatedAt          string                 `json:"created_at"`
	PreviousStartAt    string                 `json:"previous_start_at,omitempty"`
	PreviousEndAt      string                 `json:"previous_end_at,omitempty"`
	PaymentStatus      string                 `json:"payment_status,omitempty"`
	AmountPaidCents    int                    `json:"amount_paid_cents,omitempty"`
	AmountPaidCurrency string                 `json:"amount_paid_currency,omitempty"`
	AttendeeName       string                 `json:"attendee_name,omitempty"`
	AttendeeEmail      string                 `json:"attendee_email,omitempty"`
	ContactPhone       string                 `json:"contact_phone,omitempty"`
	Notes              string                 `json:"notes,omitempty"`
	Guests             []string               `json:"guests,omitempty"`
	Answers            []BookingAnswerPayload `json:"answers,omitempty"`
	// RSVPStatus is the booker's answer to a Calnode-sent invite (accepted, declined,
	// tentative); set on booking.rsvp only.
	RSVPStatus string `json:"rsvp_status,omitempty"`
}

type Service struct {
	db  *sql.DB
	key [32]byte
}

// New creates a Service. If encKeyHex is empty an ephemeral key is generated
// (secrets won't survive restarts but the server still works in dev/test).
func New(db *sql.DB, encKeyHex string) (*Service, error) {
	s := &Service{db: db}
	if encKeyHex != "" {
		b, err := hex.DecodeString(encKeyHex)
		if err != nil || len(b) != 32 {
			return nil, fmt.Errorf("webhook: encryption key must be 64 hex chars")
		}
		copy(s.key[:], b)
	} else {
		if _, err := io.ReadFull(rand.Reader, s.key[:]); err != nil {
			return nil, fmt.Errorf("webhook: generate ephemeral key: %w", err)
		}
	}
	return s, nil
}

// Create registers a new webhook and returns the Webhook plus the plain-text
// signing secret (shown only once; stored encrypted).
// Create registers a webhook (fields default to the unset/original-payload set;
// callers set field selection via Update).
type Options struct {
	Fields      []string
	EventTypeID *string
	Scopes      *[]Scope
	Permissions access.Grants
}

func (s *Service) Create(ctx context.Context, userID, url string, events []string) (*Webhook, string, error) {
	return s.CreateConfigured(ctx, userID, url, events, Options{})
}
func (s *Service) CreateConfigured(ctx context.Context, userID, url string, events []string, options Options) (*Webhook, string, error) {
	if options.EventTypeID != nil && *options.EventTypeID == "" {
		options.EventTypeID = nil // legacy clients use an empty string for account scope
	}
	rawSecret := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, rawSecret); err != nil {
		return nil, "", fmt.Errorf("webhook: generate secret: %w", err)
	}
	plainSecret := hex.EncodeToString(rawSecret)

	encSecret, err := s.encrypt(rawSecret)
	if err != nil {
		return nil, "", fmt.Errorf("webhook: encrypt secret: %w", err)
	}

	eventsJSON, err := json.Marshal(events)
	if err != nil {
		return nil, "", fmt.Errorf("webhook: marshal events: %w", err)
	}

	grants, grantErr := access.Load(ctx, s.db, userID)
	if grantErr != nil {
		return nil, "", grantErr
	}
	if options.Permissions != nil {
		grants = access.Intersect(grants, options.Permissions)
	}
	var fields, scopes any
	if options.Fields != nil {
		raw, _ := json.Marshal(ValidFields(options.Fields))
		fields = string(raw)
	}
	if options.Scopes != nil {
		raw, _ := json.Marshal(*options.Scopes)
		scopes = string(raw)
	}
	id := uid.New()
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO webhooks (id, user_id, url, events, secret_enc,permissions_json,fields,event_type_id,scopes_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, userID, url, string(eventsJSON), encSecret, grants.JSON(), fields, options.EventTypeID, scopes)
	if err != nil {
		return nil, "", fmt.Errorf("webhook: insert: %w", err)
	}

	wh := &Webhook{
		ID:        id,
		UserID:    userID,
		URL:       url,
		Events:    events,
		IsActive:  true,
		CreatedAt: time.Now().UTC(),
	}
	wh.Fields = options.Fields
	wh.EventTypeID = options.EventTypeID
	if options.Scopes != nil {
		wh.Scopes = *options.Scopes
	}
	return wh, plainSecret, nil
}

// Update applies partial changes to a webhook owned by userID. Nil pointers are
// left unchanged. Returns ErrNotFound if no such webhook exists for the user.
func (s *Service) Update(ctx context.Context, userID, id string, events, fields *[]string) error {
	return s.UpdateConfig(ctx, userID, id, events, fields, nil, nil)
}
func (s *Service) UpdateConfig(ctx context.Context, userID, id string, events, fields *[]string, legacy *string, scopes *[]Scope) error {
	var set []string
	var args []any
	if events != nil {
		eb, _ := json.Marshal(*events)
		set = append(set, "events = ?")
		args = append(args, string(eb))
	}
	if fields != nil {
		fb, _ := json.Marshal(ValidFields(*fields))
		set = append(set, "fields = ?")
		args = append(args, string(fb))
	}
	if scopes != nil {
		raw, _ := json.Marshal(*scopes)
		set = append(set, "scopes_json = ?", "event_type_id = NULL")
		args = append(args, string(raw))
	} else if legacy != nil {
		var value any
		if *legacy != "" {
			value = *legacy
		}
		set = append(set, "event_type_id = ?", "scopes_json = NULL")
		args = append(args, value)
	}
	if len(set) == 0 {
		return nil
	}
	args = append(args, id, userID)
	res, err := s.db.ExecContext(ctx,
		`UPDATE webhooks SET `+strings.Join(set, ", ")+` WHERE id = ? AND user_id = ?`, args...) // #nosec G202 -- set is built above from hardcoded "col = ?" literals only; every value is bound via args...
	if err != nil {
		return fmt.Errorf("webhook: update: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetEventTypeID scopes an existing webhook to a single event type (or nil/"" for global).
func (s *Service) SetEventTypeID(ctx context.Context, userID, id string, eventTypeID *string) error {
	var val any
	if eventTypeID != nil && *eventTypeID != "" {
		val = *eventTypeID
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE webhooks SET event_type_id = ? WHERE id = ? AND user_id = ?`,
		val, id, userID)
	if err != nil {
		return fmt.Errorf("webhook: set event_type_id: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// List returns all webhooks for a user, most recent first.
func (s *Service) List(ctx context.Context, userID string) ([]Webhook, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, url, events, fields, is_active, created_at, event_type_id, scopes_json
		FROM webhooks WHERE user_id = ? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("webhook: list: %w", err)
	}
	defer rows.Close()

	var out []Webhook
	for rows.Next() {
		var wh Webhook
		var eventsJSON string
		var fieldsJSON, eventTypeID, scopesJSON sql.NullString
		var isActive int
		var createdAt string
		if err := rows.Scan(&wh.ID, &wh.URL, &eventsJSON, &fieldsJSON, &isActive, &createdAt, &eventTypeID, &scopesJSON); err != nil {
			return nil, fmt.Errorf("webhook: scan: %w", err)
		}
		if scopesJSON.Valid {
			_ = json.Unmarshal([]byte(scopesJSON.String), &wh.Scopes)
		}
		wh.UserID = userID
		wh.IsActive = isActive == 1
		if eventTypeID.Valid && eventTypeID.String != "" {
			et := eventTypeID.String
			wh.EventTypeID = &et
		}
		_ = json.Unmarshal([]byte(eventsJSON), &wh.Events)
		if fieldsJSON.Valid && fieldsJSON.String != "" {
			_ = json.Unmarshal([]byte(fieldsJSON.String), &wh.Fields)
		} else {
			wh.Fields = defaultFields // surface the effective set for unconfigured webhooks
		}
		if t, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
			wh.CreatedAt = t
		}
		out = append(out, wh)
	}
	return out, rows.Err()
}

// Delete removes a webhook owned by userID. Returns ErrNotFound if it doesn't exist.
func (s *Service) Delete(ctx context.Context, userID, id string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM webhooks WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return fmt.Errorf("webhook: delete: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// enrichedBooking is the full set of values available to a webhook payload,
// gathered once per Enqueue and then filtered per-webhook by its field list.
type enrichedBooking struct {
	eventTypeID                              string
	linkID, linkToken, linkExpires, linkUsed string
	core                                     BookingPayload
	eventTypeName                            string
	hostName, hostEmail                      string
	attendeeName, attendeeEmail, attendeeTZ  string
	answers                                  []map[string]string
	// inviteDelivery is who sent the booker's invite (calendar, calnode or none), read
	// from the booking row so every event carries it without each caller passing it.
	inviteDelivery string
}

// enrich loads the data not carried in BookingPayload (host name/email, event-type
// name, attendee identity, intake answers, invite delivery) from the booking row. Best-effort: any
// query failure simply leaves those fields empty. Queries are drained sequentially,
// safe on the single-connection pool, and run before Enqueue opens its tx.
func (s *Service) enrich(ctx context.Context, p BookingPayload) enrichedBooking {
	bd := enrichedBooking{core: p, answers: []map[string]string{}}
	var currentLocation string
	if p.ID == "" {
		return bd
	}
	_ = s.db.QueryRowContext(ctx, `
		SELECT et.id,COALESCE(et.name,''), COALESCE(u.name,''), COALESCE(u.email,''), b.invite_delivery,COALESCE(b.location_value,'')
		FROM bookings b
		JOIN event_types et ON et.id = b.event_type_id
		JOIN users u ON u.id = b.host_id
		WHERE b.id = ?`, p.ID).Scan(&bd.eventTypeID, &bd.eventTypeName, &bd.hostName, &bd.hostEmail, &bd.inviteDelivery, &currentLocation)

	_ = s.db.QueryRowContext(ctx, `SELECT id,token,COALESCE(expires_at,''),COALESCE(used_at,'') FROM single_use_tokens WHERE booking_id=?`, p.ID).Scan(&bd.linkID, &bd.linkToken, &bd.linkExpires, &bd.linkUsed)
	if bd.core.LocationValue == "" {
		bd.core.LocationValue = currentLocation
	}
	_ = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(name,''), COALESCE(email,''), COALESCE(iana_timezone,'')
		FROM booking_attendees WHERE booking_id = ? AND is_organizer = 1`, p.ID).
		Scan(&bd.attendeeName, &bd.attendeeEmail, &bd.attendeeTZ)

	rows, err := s.db.QueryContext(ctx, `
		SELECT q.label, ba.value
		FROM booking_answers ba
		JOIN event_type_questions q ON q.id = ba.question_id
		WHERE ba.booking_id = ?
		ORDER BY q.position`, p.ID)
	if err == nil {
		for rows.Next() {
			var label, value string
			if err := rows.Scan(&label, &value); err == nil {
				bd.answers = append(bd.answers, map[string]string{"question": label, "answer": value})
			}
		}
		rows.Close() // #nosec G104 -- rows already fully consumed above; nothing actionable on close error
	}
	return bd
}

// buildData renders the "data" object containing only the requested field keys.
// The four optional booking fields are omitted when empty (matching the original
// payload's omitempty behaviour); everything else is always included when selected.
func buildData(bd enrichedBooking, fields []string) map[string]any {
	always := map[string]any{
		FieldID:            bd.core.ID,
		FieldStatus:        bd.core.Status,
		FieldStartAt:       bd.core.StartAt,
		FieldEndAt:         bd.core.EndAt,
		FieldCreatedAt:     bd.core.CreatedAt,
		FieldEventTypeSlug: bd.core.EventTypeSlug,
		FieldEventTypeID:   bd.eventTypeID,
		FieldBookingID:     bd.core.ID,
		FieldEventTypeName: bd.eventTypeName,
		FieldHostID:        bd.core.HostID,
		FieldHostName:      bd.hostName,
		FieldHostEmail:     bd.hostEmail,
		FieldAttendeeName:  bd.attendeeName,
		FieldAttendeeEmail: bd.attendeeEmail,
		FieldAttendeeTZ:    bd.attendeeTZ,
		FieldAnswers:       bd.answers,
	}
	omitEmpty := map[string]string{
		FieldLocation:        bd.core.LocationValue,
		FieldCancelReason:    bd.core.CancellationReason,
		FieldPreviousStartAt: bd.core.PreviousStartAt,
		FieldPreviousEndAt:   bd.core.PreviousEndAt,
		FieldPaymentStatus:   bd.core.PaymentStatus,
		FieldCurrency:        bd.core.AmountPaidCurrency,
		FieldRSVPStatus:      bd.core.RSVPStatus,
		FieldInviteDelivery:  bd.inviteDelivery,
		FieldSingleUseLinkID: bd.linkID, FieldToken: bd.linkToken, FieldExpiresAt: bd.linkExpires, FieldUsedAt: bd.linkUsed,
	}
	out := make(map[string]any, len(fields))
	for _, f := range fields {
		if v, ok := always[f]; ok {
			out[f] = v
		} else if v, ok := omitEmpty[f]; ok && v != "" {
			out[f] = v
		} else if f == FieldAmountPaid && bd.core.AmountPaidCents > 0 {
			out[f] = bd.core.AmountPaidCents
		}
	}
	return out
}

// Enqueue finds active legacy-account or multi-scope webhooks subscribed to event,
// and creates a webhook_deliveries + jobs row pair for each. Failures are
// errors returned to the durable outbox worker for retry.
func (s *Service) Enqueue(ctx context.Context, event string, p BookingPayload) error {
	return s.EnqueueEvent(ctx, event, p, "")
}

// EnqueueEvent keeps one delivery identity per webhook and durable source event.
// Retrying an outbox expansion cannot emit a second logical notification.
func (s *Service) EnqueueEvent(ctx context.Context, event string, p BookingPayload, sourceID string) error {
	var eventTypeID string
	if p.EventTypeSlug != "" {
		_ = s.db.QueryRowContext(ctx, `SELECT id FROM event_types WHERE slug = ?`, p.EventTypeSlug).Scan(&eventTypeID)
	}
	if eventTypeID == "" && p.ID != "" {
		_ = s.db.QueryRowContext(ctx, `SELECT event_type_id FROM bookings WHERE id = ?`, p.ID).Scan(&eventTypeID)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, events, fields FROM webhooks
		WHERE is_active = 1 AND (scopes_json IS NOT NULL OR ((user_id = ? OR event_type_id IS NOT NULL)
		  AND (event_type_id IS NULL OR (? != '' AND event_type_id = ?))))`,
		p.HostID, eventTypeID, eventTypeID)
	if err != nil {
		return fmt.Errorf("webhook: list for enqueue: %w", err)
	}

	type wrow struct {
		id     string
		fields []string // nil = default set
	}
	var matching []wrow
	for rows.Next() {
		var id, eventsJSON string
		var fieldsJSON sql.NullString
		if err := rows.Scan(&id, &eventsJSON, &fieldsJSON); err != nil {
			rows.Close() // #nosec G104 -- already returning the scan error; nothing more actionable
			return fmt.Errorf("webhook: scan: %w", err)
		}
		var events []string
		_ = json.Unmarshal([]byte(eventsJSON), &events)
		subscribed := false
		for _, e := range events {
			if e == event {
				subscribed = true
				break
			}
		}
		if !subscribed {
			continue
		}
		var fields []string
		if fieldsJSON.Valid && fieldsJSON.String != "" {
			_ = json.Unmarshal([]byte(fieldsJSON.String), &fields)
		}
		matching = append(matching, wrow{id: id, fields: fields})
	}
	rows.Close() // #nosec G104 -- rows already fully consumed above; nothing actionable on close error
	if err := rows.Err(); err != nil {
		return err
	}
	allowed := matching[:0]
	for _, wh := range matching {
		hasAnswers := false
		for _, field := range wh.fields {
			if field == FieldAnswers {
				hasAnswers = true
			}
		}
		if s.DeliveryAuthorized(ctx, wh.id, p.ID, eventTypeID, event) && (!hasAnswers || s.DeliveryAuthorized(ctx, wh.id, p.ID, eventTypeID, "booking.answers")) {
			allowed = append(allowed, wh)
		}
	}
	matching = allowed
	if len(matching) == 0 {
		return nil
	}

	// Gather all available data once; each webhook gets its own field-filtered copy.
	bd := s.enrich(ctx, p)
	createdAt := time.Now().UTC().Format(time.RFC3339)

	// booking_id is a nullable FK; use NULL when empty so callers without a real
	// bookings row don't violate the constraint.
	var bookingIDArg interface{}
	if p.ID != "" {
		bookingIDArg = p.ID
	}

	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("webhook: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	retryStartedAt := now
	if sourceID != "" {
		if _, err := time.Parse(time.RFC3339Nano, p.OccurredAt); err == nil {
			retryStartedAt = p.OccurredAt
		}
	}

	for _, wh := range matching {
		fieldset := wh.fields
		if fieldset == nil {
			fieldset = defaultFields
		}
		envelope := map[string]any{
			"event":      event,
			"created_at": createdAt,
			"data":       buildData(bd, fieldset),
		}
		if sourceID != "" {
			envelope["event_id"] = sourceID
			envelope["occurred_at"] = p.OccurredAt
		}
		payloadBytes, err := json.Marshal(envelope)
		if err != nil {
			return fmt.Errorf("webhook: marshal payload: %w", err)
		}

		deliveryID := uid.New()
		jobID := uid.New()
		var source any
		if sourceID != "" {
			source = sourceID
		}
		res, err := tx.ExecContext(ctx, `
		 INSERT INTO webhook_deliveries(id,webhook_id,booking_id,event,payload,status,source_event_id,event_type_id,resource_id)
		 VALUES(?,?,?,?,?,'pending',?,?,?) ON CONFLICT(webhook_id,source_event_id) DO NOTHING`, deliveryID, wh.id, bookingIDArg, event, string(payloadBytes), source, eventTypeID, p.ID)
		if err != nil {
			return fmt.Errorf("webhook: insert delivery: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			continue
		}
		jobPayload, _ := json.Marshal(map[string]string{"webhook_delivery_id": deliveryID})
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO jobs (id, type, payload, run_at, max_attempts,created_at)
			VALUES (?, 'webhook.deliver', ?, ?, 24,?)`,
			jobID, string(jobPayload), now, retryStartedAt); err != nil {
			return fmt.Errorf("webhook: insert job: %w", err)
		}
	}
	return tx.Commit()
}

// SingleUsePayload is the "data" object inside single_use_link.* webhook deliveries.
type SingleUsePayload struct {
	ID            string `json:"id"`
	Token         string `json:"token"`
	EventTypeID   string `json:"event_type_id"`
	EventTypeSlug string `json:"event_type_slug"`
	EventTypeName string `json:"event_type_name"`
	HostID        string `json:"host_id"`
	BookingID     string `json:"booking_id,omitempty"`
	ExpiresAt     string `json:"expires_at,omitempty"`
	UsedAt        string `json:"used_at,omitempty"`
}

// EnqueueSingleUse finds all active webhooks for p.HostID (global or scoped to
// p.EventTypeID) subscribed to event ("single_use_link.booked" or
// "single_use_link.expired") and enqueues delivery jobs.
func (s *Service) EnqueueSingleUse(ctx context.Context, event string, p SingleUsePayload) error {
	return s.enqueueSingleUse(ctx, event, p, false)
}

// EnqueueSingleUseOnce commits the lifecycle marker and durable jobs together.
func (s *Service) EnqueueSingleUseOnce(ctx context.Context, event string, p SingleUsePayload) error {
	return s.enqueueSingleUse(ctx, event, p, true)
}
func (s *Service) enqueueSingleUse(ctx context.Context, event string, p SingleUsePayload, once bool) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, events, fields FROM webhooks
		WHERE is_active = 1 AND (scopes_json IS NOT NULL OR ((user_id = ? OR event_type_id IS NOT NULL)
		  AND (event_type_id IS NULL OR (? != '' AND event_type_id = ?))))`,
		p.HostID, p.EventTypeID, p.EventTypeID)
	if err != nil {
		return fmt.Errorf("webhook: list single-use for enqueue: %w", err)
	}

	type target struct {
		id     string
		fields []string
	}
	var matching []target
	for rows.Next() {
		var id, eventsJSON string
		var fieldsJSON sql.NullString
		if err := rows.Scan(&id, &eventsJSON, &fieldsJSON); err != nil {
			rows.Close() // #nosec G104 -- already returning scan error
			return fmt.Errorf("webhook: scan single-use: %w", err)
		}
		var events []string
		_ = json.Unmarshal([]byte(eventsJSON), &events)
		for _, e := range events {
			if e == event {
				var fields []string
				if fieldsJSON.Valid {
					if err := json.Unmarshal([]byte(fieldsJSON.String), &fields); err != nil {
						return err
					}
				}
				matching = append(matching, target{id, fields})
				break
			}
		}
	}
	rows.Close() // #nosec G104 -- rows already consumed
	if err := rows.Err(); err != nil {
		return err
	}
	allowed := matching[:0]
	for _, id := range matching {
		if s.DeliveryAuthorized(ctx, id.id, p.BookingID, p.EventTypeID, event, p.ID) {
			allowed = append(allowed, id)
		}
	}
	matching = allowed

	now := time.Now().UTC().Format(time.RFC3339)

	var bookingIDArg any
	if p.BookingID != "" {
		bookingIDArg = p.BookingID
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("webhook: begin single-use tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	if once {
		marker, condition := "booked_notified_at", "used_at IS NOT NULL"
		if event == "single_use_link.expired" {
			marker = "expired_notified_at"
			condition = "used_at IS NULL AND expires_at IS NOT NULL AND datetime(expires_at)<=datetime('now')"
		} else if event != "single_use_link.booked" {
			return errors.New("unknown single-use lifecycle event")
		}
		result, err := tx.ExecContext(ctx, `UPDATE single_use_tokens SET `+marker+`=? WHERE id=? AND `+marker+` IS NULL AND `+condition, now, p.ID)
		if err != nil {
			return err
		}
		n, _ := result.RowsAffected()
		if n == 0 {
			return nil
		}
	}

	for _, wh := range matching {
		data := singleUseData(p, wh.fields)
		payloadBytes, err := json.Marshal(map[string]any{"event": event, "created_at": now, "data": data})
		if err != nil {
			return err
		}
		whID := wh.id
		deliveryID := uid.New()
		jobID := uid.New()
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO webhook_deliveries (id, webhook_id, booking_id, event, payload, status,event_type_id,resource_id)
			VALUES (?, ?, ?, ?, ?, 'pending',?,?)`,
			deliveryID, whID, bookingIDArg, event, string(payloadBytes), p.EventTypeID, p.ID); err != nil {
			return fmt.Errorf("webhook: insert single-use delivery: %w", err)
		}
		jobPayload, _ := json.Marshal(map[string]string{"webhook_delivery_id": deliveryID})
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO jobs (id, type, payload, run_at, max_attempts)
			VALUES (?, 'webhook.deliver', ?, ?, 24)`,
			jobID, string(jobPayload), now); err != nil {
			return fmt.Errorf("webhook: insert single-use job: %w", err)
		}
	}
	return tx.Commit()
}

// ListDeliveries returns the 50 most recent deliveries for a webhook.
func (s *Service) ListDeliveries(ctx context.Context, userID, webhookID string) ([]Delivery, error) {
	var ownerID string
	if err := s.db.QueryRowContext(ctx,
		`SELECT user_id FROM webhooks WHERE id = ?`, webhookID).Scan(&ownerID); err != nil {
		return nil, ErrNotFound
	}
	if ownerID != userID {
		return nil, ErrNotFound
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, COALESCE(booking_id,''), event, status,
		       response_status, attempt_count, last_attempted_at,COALESCE(last_error,''),
 (SELECT j.run_at FROM jobs j WHERE j.type='webhook.deliver' AND j.status='pending' AND json_extract(j.payload,'$.webhook_delivery_id')=webhook_deliveries.id LIMIT 1)
		FROM webhook_deliveries WHERE webhook_id = ?
		ORDER BY rowid DESC LIMIT 50`, webhookID)
	if err != nil {
		return nil, fmt.Errorf("webhook: list deliveries: %w", err)
	}
	defer rows.Close()

	var out []Delivery
	for rows.Next() {
		var d Delivery
		var respStatus sql.NullInt64
		var lastAt, nextAt sql.NullString
		if err := rows.Scan(&d.ID, &d.BookingID, &d.Event, &d.Status,
			&respStatus, &d.AttemptCount, &lastAt, &d.LastError, &nextAt); err != nil {
			return nil, fmt.Errorf("webhook: scan delivery: %w", err)
		}
		if nextAt.Valid {
			v := nextAt.String
			d.NextAttemptAt = &v
		}
		d.WebhookID = webhookID
		if respStatus.Valid {
			v := int(respStatus.Int64)
			d.ResponseStatus = &v
		}
		if lastAt.Valid && lastAt.String != "" {
			s := lastAt.String
			d.LastAttemptedAt = &s
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DecryptSecret decrypts the stored secret_enc for use by the worker when signing deliveries.
func (s *Service) DecryptSecret(encSecret string) ([]byte, error) {
	return s.decrypt(encSecret)
}

// Sign returns the HMAC-SHA256 signature header value for a payload.
func Sign(secret, payload []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func (s *Service) encrypt(plaintext []byte) (string, error) {
	block, err := aes.NewCipher(s.key[:])
	if err != nil {
		return "", fmt.Errorf("webhook: cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("webhook: gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("webhook: nonce: %w", err)
	}
	sealed := gcm.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

func (s *Service) decrypt(encoded string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("webhook: base64: %w", err)
	}
	block, err := aes.NewCipher(s.key[:])
	if err != nil {
		return nil, fmt.Errorf("webhook: cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("webhook: gcm: %w", err)
	}
	ns := gcm.NonceSize()
	if len(b) < ns {
		return nil, fmt.Errorf("webhook: ciphertext too short")
	}
	plain, err := gcm.Open(nil, b[:ns], b[ns:], nil)
	if err != nil {
		return nil, fmt.Errorf("webhook: decrypt: %w", err)
	}
	return plain, nil
}

// nil preserves the historical lifecycle schema; [] means send no data.
func singleUseData(p SingleUsePayload, fields []string) map[string]any {
	raw, _ := json.Marshal(p)
	var data map[string]any
	_ = json.Unmarshal(raw, &data)
	if fields == nil {
		return data
	}
	data[FieldSingleUseLinkID] = p.ID
	out := map[string]any{}
	for _, field := range fields {
		if v, ok := data[field]; ok {
			out[field] = v
		}
	}
	return out
}

// RetryDelivery resets a failed delivery without changing its id or signed body.
func (s *Service) RetryDelivery(ctx context.Context, owner, id, deliveryID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status string
	if err = tx.QueryRowContext(ctx, `SELECT d.status FROM webhook_deliveries d JOIN webhooks w ON w.id=d.webhook_id WHERE d.id=? AND w.id=? AND w.user_id=?`, deliveryID, id, owner).Scan(&status); err != nil {
		return ErrNotFound
	}
	if status != "failed" {
		return fmt.Errorf("only failed deliveries can be retried")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx, `UPDATE jobs SET status='pending',attempts=0,max_attempts=24,run_at=?,created_at=?,finished_at=NULL,locked_until=NULL,last_error=NULL WHERE type='webhook.deliver' AND json_extract(payload,'$.webhook_delivery_id')=? AND status IN ('failed','done')`, now, now, deliveryID)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return fmt.Errorf("delivery job is unavailable or already running")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE webhook_deliveries SET status='pending',last_error=NULL WHERE id=?`, deliveryID); err != nil {
		return err
	}
	return tx.Commit()
}
