package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/mailer"
	"github.com/calnode/calnode/internal/uid"
)

type emailTemplateJSON struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	IsDefault bool   `json:"is_default"`
	Mode      string `json:"mode"`
	Subject   string `json:"subject"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func validEmailTemplateKind(k string) bool {
	return k == "confirmation" || k == "cancellation" || k == "reschedule" || k == "reminder"
}

type emailDraft struct {
	Mode    string `json:"mode"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

func (draft emailDraft) apply(d *mailer.BookingData) error {
	d.SubjectOverride = strings.TrimSpace(draft.Subject)
	d.CustomHTML, d.CustomNote = "", ""
	switch draft.Mode {
	case "", "note":
		d.CustomNote = draft.Body
	case "html":
		if strings.TrimSpace(draft.Body) != "" {
			cleaned, err := mailer.SanitizeAndValidateEmailHTML(draft.Body)
			if err != nil {
				return err
			}
			d.CustomHTML = cleaned
		}
	default:
		return errors.New("mode must be 'note' or 'html'")
	}
	return nil
}

// PreviewEmailTemplate handles POST /v1/event-types/{slug}/email-preview.
// Renders the email (default or custom note/HTML) using sample booking data without
// sending any email, returning { subject, html, default_html }.
func (h *Handler) PreviewEmailTemplate(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	slug := r.PathValue("slug")

	r.Body = http.MaxBytesReader(w, r.Body, 128<<10)
	var req struct {
		Type    string `json:"type"`
		Mode    string `json:"mode"`
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if !validEmailTemplateKind(req.Type) {
		h.writeError(w, http.StatusBadRequest, "type must be confirmation, cancellation, reschedule, or reminder")
		return
	}
	if req.Mode == "" {
		req.Mode = "note"
	}
	if req.Mode != "note" && req.Mode != "html" {
		h.writeError(w, http.StatusBadRequest, "mode must be 'note' or 'html'")
		return
	}

	ownerID, ownerName, ownerEmail, etName := user.ID, user.Name, user.Email, "Sample meeting"
	durationMinutes := 30
	var locVal sql.NullString
	if slug != "" {
		err := h.db.QueryRowContext(r.Context(), `
 SELECT et.user_id,u.name,u.email,et.name,et.duration_minutes,et.location_value
 FROM event_types et JOIN users u ON u.id=et.user_id WHERE et.slug=?`, slug).
			Scan(&ownerID, &ownerName, &ownerEmail, &etName, &durationMinutes, &locVal)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && !canManageEventType(user, ownerID)) {
			h.writeError(w, 404, "event type not found")
			return
		}
		if err != nil {
			h.writeError(w, 500, "could not load event type")
			return
		}
	}

	if durationMinutes < 1 {
		durationMinutes = 30
	}
	start := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, 1).Add(14 * time.Hour)
	end := start.Add(time.Duration(durationMinutes) * time.Minute)
	locStr := locVal.String
	if locStr == "" {
		locStr = "Google Meet"
	}

	d := mailer.BookingData{
		BookingID:         "preview-sample",
		EventTypeName:     etName,
		EventTypeSlug:     slug,
		HostName:          ownerName,
		HostEmail:         ownerEmail,
		OrganizerName:     "Alex Johnson",
		OrganizerEmail:    "alex@example.com",
		OrganizerTimezone: user.IANATZ,
		StartAt:           start,
		EndAt:             end,
		PreviousStartAt:   start.AddDate(0, 0, -1),
		PreviousEndAt:     end.AddDate(0, 0, -1),
		LocationValue:     locStr,
		ManageURL:         h.publicURL() + "/manage/sample-token",
		BaseURL:           h.publicURL(),
		SubjectOverride:   strings.TrimSpace(req.Subject),
	}
	h.applyBranding(r.Context(), &d)

	// Always compute the default HTML so the UI can pre-fill Custom HTML from it.
	dDefault := d
	dDefault.CustomNote = req.Body
	if req.Mode == "html" {
		dDefault.CustomNote = ""
	}
	_, _, defaultHTML, _ := mailer.RenderBody(req.Type, dDefault)

	if err := (emailDraft{Mode: req.Mode, Subject: req.Subject, Body: req.Body}).apply(&d); err != nil {
		h.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	subject, _, renderedHTML, _ := mailer.RenderBody(req.Type, d)
	h.writeJSON(w, http.StatusOK, map[string]any{
		"subject":      subject,
		"html":         renderedHTML,
		"default_html": defaultHTML,
	})
}

// ListEmailTemplates handles GET /v1/email-templates.
func (h *Handler) ListEmailTemplates(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	var rows *sql.Rows
	var err error
	if kind != "" {
		rows, err = h.db.QueryContext(r.Context(), `
			SELECT id, name, kind, is_default, mode, subject, body, created_at, updated_at
			FROM email_templates WHERE user_id = ? AND kind = ?
			ORDER BY is_default DESC, name ASC`, user.ID, kind)
	} else {
		rows, err = h.db.QueryContext(r.Context(), `
			SELECT id, name, kind, is_default, mode, subject, body, created_at, updated_at
			FROM email_templates WHERE user_id = ?
			ORDER BY kind ASC, is_default DESC, name ASC`, user.ID)
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list email templates", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	out := []emailTemplateJSON{}
	for rows.Next() {
		var t emailTemplateJSON
		var isDef int
		if err := rows.Scan(&t.ID, &t.Name, &t.Kind, &isDef, &t.Mode, &t.Subject, &t.Body, &t.CreatedAt, &t.UpdatedAt); err != nil {
			continue
		}
		t.IsDefault = isDef != 0
		out = append(out, t)
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

// CreateEmailTemplate handles POST /v1/email-templates.
func (h *Handler) CreateEmailTemplate(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 128<<10)
	var req struct {
		Name      string `json:"name"`
		Kind      string `json:"kind"`
		IsDefault bool   `json:"is_default"`
		Mode      string `json:"mode"`
		Subject   string `json:"subject"`
		Body      string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		h.writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if !validEmailTemplateKind(req.Kind) {
		h.writeError(w, http.StatusBadRequest, "invalid kind")
		return
	}
	if req.Mode != "note" && req.Mode != "html" {
		h.writeError(w, http.StatusBadRequest, "mode must be 'note' or 'html'")
		return
	}
	body := req.Body
	if req.Mode == "html" {
		cleaned, err := mailer.SanitizeAndValidateEmailHTML(body)
		if err != nil {
			h.writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		body = cleaned
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	id := uid.New()
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback() //nolint:errcheck

	if req.IsDefault {
		if _, err := tx.ExecContext(r.Context(),
			`UPDATE email_templates SET is_default = 0 WHERE user_id = ? AND kind = ?`,
			user.ID, req.Kind); err != nil {
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}
	isDefInt := 0
	if req.IsDefault {
		isDefInt = 1
	}
	if _, err := tx.ExecContext(r.Context(), `
		INSERT INTO email_templates (id, user_id, name, kind, is_default, mode, subject, body, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, user.ID, req.Name, req.Kind, isDefInt, req.Mode, strings.TrimSpace(req.Subject), body, now, now); err != nil {
		h.logger.ErrorContext(r.Context(), "create email template", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := tx.Commit(); err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.writeJSON(w, http.StatusCreated, emailTemplateJSON{
		ID:        id,
		Name:      req.Name,
		Kind:      req.Kind,
		IsDefault: req.IsDefault,
		Mode:      req.Mode,
		Subject:   strings.TrimSpace(req.Subject),
		Body:      body,
		CreatedAt: now,
		UpdatedAt: now,
	})
}

// DeleteEmailTemplate handles DELETE /v1/email-templates/{id}.
func (h *Handler) DeleteEmailTemplate(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id := r.PathValue("id")
	res, err := h.db.ExecContext(r.Context(),
		`DELETE FROM email_templates WHERE id = ? AND user_id = ?`, id, user.ID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		h.writeError(w, http.StatusNotFound, "template not found")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// PreviewSavedEmailTemplate uses the same renderer and sanitizer without an event dependency.
func (h *Handler) PreviewSavedEmailTemplate(w http.ResponseWriter, r *http.Request) {
	h.PreviewEmailTemplate(w, r)
}

func (h *Handler) UpdateEmailTemplate(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	var req struct {
		Name      string `json:"name"`
		Kind      string `json:"kind"`
		IsDefault bool   `json:"is_default"`
		Mode      string `json:"mode"`
		Subject   string `json:"subject"`
		Body      string `json:"body"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&req) != nil {
		h.writeError(w, 400, "invalid JSON")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 120 || !validEmailTemplateKind(req.Kind) || (req.Mode != "note" && req.Mode != "html") {
		h.writeError(w, 400, "valid name, kind and mode are required")
		return
	}
	if req.Mode == "html" {
		clean, err := mailer.SanitizeAndValidateEmailHTML(req.Body)
		if err != nil {
			h.writeError(w, 400, err.Error())
			return
		}
		req.Body = clean
	}
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.writeError(w, 500, "could not save template")
		return
	}
	defer tx.Rollback()
	var created string
	if tx.QueryRowContext(r.Context(), `SELECT created_at FROM email_templates WHERE id=? AND user_id=?`, r.PathValue("id"), user.ID).Scan(&created) != nil {
		h.writeError(w, 404, "template not found")
		return
	}
	if req.IsDefault {
		if _, err = tx.ExecContext(r.Context(), `UPDATE email_templates SET is_default=0 WHERE user_id=? AND kind=?`, user.ID, req.Kind); err != nil {
			h.writeError(w, 500, "could not update default")
			return
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = tx.ExecContext(r.Context(), `UPDATE email_templates SET name=?,kind=?,is_default=?,mode=?,subject=?,body=?,updated_at=? WHERE id=? AND user_id=?`, req.Name, req.Kind, req.IsDefault, req.Mode, strings.TrimSpace(req.Subject), req.Body, now, r.PathValue("id"), user.ID); err != nil {
		h.writeError(w, 500, "could not update template")
		return
	}
	if tx.Commit() != nil {
		h.writeError(w, 500, "could not save template")
		return
	}
	h.writeJSON(w, 200, emailTemplateJSON{ID: r.PathValue("id"), Name: req.Name, Kind: req.Kind, IsDefault: req.IsDefault, Mode: req.Mode, Subject: strings.TrimSpace(req.Subject), Body: req.Body, CreatedAt: created, UpdatedAt: now})
}
