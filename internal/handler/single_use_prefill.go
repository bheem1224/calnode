package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/booking"
)

// Saved prefills are form defaults, never an authorization decision. Only the
// event's existing form identifiers and normal attendee fields are accepted.
func (h *Handler) validateLinkPrefill(ctx context.Context, eventID string, values map[string]string) (any, error) {
	if len(values) == 0 {
		return nil, nil
	}
	if len(values) > 32 {
		return nil, &answerError{"prefill may contain at most 32 fields"}
	}
	allowed := map[string]bool{"name": true, "email": true}
	rows, err := h.db.QueryContext(ctx, `SELECT identifier FROM event_type_questions WHERE event_type_id=? AND identifier IS NOT NULL`, eventID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			rows.Close()
			return nil, err
		}
		allowed[key] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for key, value := range values {
		if key == "" || !allowed[key] || len(key) > 128 || len(value) > 4096 || strings.TrimSpace(value) == "" || strings.ContainsRune(value, 0) {
			return nil, &answerError{"prefill must use existing form identifiers with nonempty values of at most 4096 bytes"}
		}
	}
	data, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	return string(data), nil
}

func (h *Handler) savedSingleUsePrefill(ctx context.Context, token, eventID string) (map[string]string, error) {
	if token == "" {
		return nil, nil
	}
	if !h.dynamicLinkAllowed(ctx, token) {
		return nil, booking.ErrSingleUseTokenGone
	}
	var raw sql.NullString
	err := h.db.QueryRowContext(ctx, `SELECT prefill_json FROM single_use_tokens WHERE token=? AND event_type_id=? AND used_at IS NULL AND (expires_at IS NULL OR datetime(expires_at)>datetime(?))`, token, eventID, time.Now().UTC().Format(time.RFC3339Nano)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, booking.ErrSingleUseTokenGone
	}
	if err != nil {
		return nil, err
	}
	var result map[string]string
	if raw.Valid && json.Unmarshal([]byte(raw.String), &result) != nil {
		return nil, fmt.Errorf("invalid saved link prefill")
	}
	return result, nil
}

type intakeAnswerJSON = struct {
	QuestionID string `json:"question_id"`
	Value      string `json:"value"`
}

// Nonempty submitted values win: these are defaults, not security claims. The
// regular required/type/duplicate-answer validators still run on the merged form.
func (h *Handler) applySavedLinkAnswers(ctx context.Context, token, eventID string, answers []intakeAnswerJSON) ([]intakeAnswerJSON, error) {
	defaults, err := h.savedSingleUsePrefill(ctx, token, eventID)
	if err != nil || len(defaults) == 0 {
		return answers, err
	}
	rows, err := h.db.QueryContext(ctx, `SELECT id,COALESCE(identifier,'') FROM event_type_questions WHERE event_type_id=?`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, key string
		if err = rows.Scan(&id, &key); err != nil {
			return nil, err
		}
		value, ok := defaults[key]
		if !ok {
			continue
		}
		found := false
		for i := range answers {
			if answers[i].QuestionID == id {
				found = true
				if strings.TrimSpace(answers[i].Value) == "" {
					answers[i].Value = value
				}
				break
			}
		}
		if !found {
			answers = append(answers, intakeAnswerJSON{id, value})
		}
	}
	return answers, rows.Err()
}
