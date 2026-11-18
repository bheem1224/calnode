package handler

import (
	"context"
	"net/http"
	"time"
)

func (h *Handler) recordCalendarHealth(ctx context.Context, id string, checkErr error) {
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
		h.securityNotice(ctx, id, "Calendar availability check failed; review your connected calendars", "")
	}
}
func (h *Handler) GetSecurityOperations(w http.ResponseWriter, r *http.Request) {
	type job struct {
		ID    string `json:"id"`
		Type  string `json:"type"`
		Error string `json:"error"`
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
	w.Header().Set("Cache-Control", "no-store")
	h.writeJSON(w, 200, map[string]any{"failed_jobs": jobs, "calendar_errors": accounts, "failed_confirmations": confirmations, "pending_jobs": pending})
}
func (h *Handler) RetrySecurityJob(w http.ResponseWriter, r *http.Request) {
	u, _ := userFromContext(r.Context())
	if !h.freshAuthFactor(w, r, u) {
		return
	}
	id := r.PathValue("id")
	res, err := h.db.ExecContext(r.Context(), `UPDATE jobs SET status='pending',attempts=0,run_at=?,last_error=NULL,locked_until=NULL,finished_at=NULL WHERE id=? AND status='failed'`, time.Now().UTC().Format(time.RFC3339), id)
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
	h.writeJSON(w, 200, map[string]bool{"ok": true})
}
