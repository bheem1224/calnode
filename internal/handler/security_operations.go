package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/uid"
)

func (h *Handler) recordCalendarHealth(ctx context.Context, id string, checkErr error) {
	// Navigating away or replacing an availability request cancels its provider
	// calls. That says nothing about the connected calendar's health.
	if errors.Is(checkErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if checkErr == nil {
		_, _ = h.db.ExecContext(ctx, `UPDATE users SET calendar_check_error='',calendar_checked_at=? WHERE id=?`, now, id)
		return
	}
	res, err := h.db.ExecContext(ctx, `UPDATE users SET calendar_check_error='Calendar availability could not be checked. Reconnect the affected calendar if this continues.',calendar_checked_at=? WHERE id=? AND calendar_check_error=''`, now, id)
	if err != nil {
		return
	}
	n, _ := res.RowsAffected()
	if n == 1 {
		payload, _ := json.Marshal(map[string]string{"user_id": id, "category": "calendar", "action": "Calendar availability check failed; review your connected calendars"})
		// A brief recovery must not reset the notification cooldown. Keep the
		// health state current, but send at most one calendar alert per six hours.
		if _, err := h.db.ExecContext(ctx, `INSERT OR IGNORE INTO jobs(id,type,payload,run_at) SELECT ?,'security.notify',?,? WHERE NOT EXISTS (SELECT 1 FROM jobs WHERE type='security.notify' AND json_extract(payload,'$.user_id')=? AND json_extract(payload,'$.category')='calendar' AND created_at>datetime('now','-6 hours'))`, uid.New(), string(payload), now, id); err != nil {
			h.logger.Error("calendar health notification queue failed", "error", err)
		}
	}
}
func (h *Handler) GetSecurityOperations(w http.ResponseWriter, r *http.Request) {
	type job struct {
		ID        string `json:"id"`
		Type      string `json:"type"`
		Error     string `json:"error"`
		BookingID string `json:"booking_id,omitempty"`
		Owner     string `json:"owner,omitempty"`
	}
	type account struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Error   string `json:"error"`
		Checked string `json:"checked_at"`
	}
	jobs := []job{}
	rows, err := h.db.QueryContext(r.Context(), `SELECT id,type,COALESCE(last_error,'') FROM jobs WHERE status='failed' ORDER BY created_at DESC LIMIT 50`)
	if err != nil {
		h.writeError(w, 500, "could not load delivery failures")
		return
	}
	for rows.Next() {
		var item job
		if err = rows.Scan(&item.ID, &item.Type, &item.Error); err != nil {
			break
		}
		jobs = append(jobs, item)
	}
	scanErr := rows.Err()
	rows.Close()
	if err != nil || scanErr != nil {
		h.writeError(w, 500, "could not load delivery failures")
		return
	}
	accounts := []account{}
	rows, err = h.db.QueryContext(r.Context(), `SELECT id,name,calendar_check_error,calendar_checked_at FROM users WHERE archived_at IS NULL AND calendar_check_error<>'' ORDER BY name LIMIT 100`)
	if err != nil {
		h.writeError(w, 500, "could not load calendar health")
		return
	}
	for rows.Next() {
		var item account
		if err = rows.Scan(&item.ID, &item.Name, &item.Error, &item.Checked); err != nil {
			break
		}
		accounts = append(accounts, item)
	}
	scanErr = rows.Err()
	rows.Close()
	if err != nil || scanErr != nil {
		h.writeError(w, 500, "could not load calendar health")
		return
	}
	var confirmations, pending int
	_ = h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM bookings WHERE confirm_failed=1 AND status='confirmed'`).Scan(&confirmations)
	_ = h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM jobs WHERE status IN('pending','running')`).Scan(&pending)
	providerWork := []job{}
	rows, err = h.db.QueryContext(r.Context(), `SELECT 'calendar-cleanup:'||c.id,'calendar.cleanup',CASE WHEN c.last_error='' THEN 'Calendar cleanup queued' ELSE c.last_error END,c.booking_id,COALESCE((SELECT name FROM users WHERE id=c.user_id),'') FROM calendar_event_cleanup c WHERE c.done=0 UNION ALL SELECT 'zoom-recovery:'||z.operation_key,'zoom.reconcile',CASE WHEN z.state='uncertain' AND z.cleanup_requested=0 THEN 'Creation outcome unresolved. Reconciliation will not create another meeting. Check Zoom; cancel and rebook if recovery remains unresolved.' WHEN z.last_error='' THEN 'Zoom cleanup queued' ELSE z.last_error END,z.booking_id,COALESCE((SELECT name FROM users WHERE id=z.user_id),'') FROM zoom_meeting_intents z WHERE (z.cleanup_requested=1 AND z.cleanup_done=0) OR (z.cleanup_requested=0 AND z.state='uncertain') LIMIT 100`)

	if err != nil {
		h.writeError(w, 500, "could not load provider recovery work")
		return
	}
	for rows.Next() {
		var item job
		if err = rows.Scan(&item.ID, &item.Type, &item.Error, &item.BookingID, &item.Owner); err != nil {
			break
		}
		providerWork = append(providerWork, item)
	}
	scanErr = rows.Err()
	rows.Close()
	if err != nil || scanErr != nil {
		h.writeError(w, 500, "could not load provider recovery work")
		return
	}
	var recoveryCount int
	if err = h.db.QueryRowContext(r.Context(), `SELECT (SELECT COUNT(*) FROM calendar_event_cleanup WHERE done=0)+(SELECT COUNT(*) FROM zoom_meeting_intents WHERE (cleanup_requested=1 AND cleanup_done=0) OR (cleanup_requested=0 AND state='uncertain'))`).Scan(&recoveryCount); err != nil {
		h.writeError(w, 500, "could not count provider recovery work")
		return
	}
	pending += recoveryCount
	w.Header().Set("Cache-Control", "no-store")
	h.writeJSON(w, 200, map[string]any{"failed_jobs": jobs, "provider_work": providerWork, "calendar_errors": accounts, "failed_confirmations": confirmations, "pending_jobs": pending})
}
func (h *Handler) RetrySecurityJob(w http.ResponseWriter, r *http.Request) {
	u, _ := userFromContext(r.Context())
	if !h.freshAuthFactor(w, r, u) {
		return
	}
	id := r.PathValue("id")
	query := `UPDATE jobs SET status='pending',attempts=0,run_at=?,last_error=NULL,locked_until=NULL,finished_at=NULL WHERE id=? AND status='failed'`
	args := []any{time.Now().UTC().Format(time.RFC3339), id}
	if strings.HasPrefix(id, "calendar-cleanup:") {
		query = `UPDATE calendar_event_cleanup SET next_attempt_at='' WHERE id=? AND done=0`
		args = []any{strings.TrimPrefix(id, "calendar-cleanup:")}
	}
	if strings.HasPrefix(id, "zoom-recovery:") {
		query = `UPDATE zoom_meeting_intents SET next_attempt_at='' WHERE operation_key=? AND ((cleanup_requested=1 AND cleanup_done=0) OR (cleanup_requested=0 AND state='uncertain'))`
		args = []any{strings.TrimPrefix(id, "zoom-recovery:")}
	}
	res, err := h.db.ExecContext(r.Context(), query, args...)
	if err != nil {
		h.writeError(w, 500, "could not retry job")
		return
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		h.writeError(w, 409, "job is no longer failed")
		return
	}
	h.securityAudit(r.Context(), u.ID, "security.job_retry", id, nil)
	h.nudgeCalendarReconcile()
	h.writeJSON(w, 200, map[string]bool{"ok": true})
}
