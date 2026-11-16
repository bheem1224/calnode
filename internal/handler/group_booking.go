package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/slots"
	"github.com/calnode/calnode/internal/uid"
)

type groupHostInfo struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Handle string `json:"handle"`
	TZ     string `json:"timezone"`
}

type groupOOONotice struct {
	HostName       string `json:"host_name"`
	Reason         string `json:"reason"`
	PublicNote     string `json:"public_note"`
	RedirectHandle string `json:"redirect_handle,omitempty"`
	RedirectName   string `json:"redirect_name,omitempty"`
}

func parseGroupHandles(raw string) []string {
	parts := strings.Split(raw, "+")
	seen := make(map[string]bool, len(parts))
	var out []string
	for _, p := range parts {
		h := strings.TrimSpace(strings.ToLower(p))
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	return out
}

func (h *Handler) resolveGroupHosts(ctx context.Context, rawHandles string) ([]groupHostInfo, error) {
	handles := parseGroupHandles(rawHandles)
	if len(handles) == 0 || len(handles) > 10 {
		return nil, sql.ErrNoRows
	}
	hosts := make([]groupHostInfo, 0, len(handles))
	for _, handle := range handles {
		var u groupHostInfo
		err := h.db.QueryRowContext(ctx, `
			SELECT id, name, COALESCE(handle, ''), iana_timezone
			FROM users
			WHERE LOWER(handle) = ? AND archived_at IS NULL`, handle).
			Scan(&u.ID, &u.Name, &u.Handle, &u.TZ)
		if err != nil {
			return nil, err
		}
		hosts = append(hosts, u)
	}
	return hosts, nil
}

func (h *Handler) groupOOONotices(ctx context.Context, hosts []groupHostInfo, dateFrom, dateTo time.Time) []groupOOONotice {
	fromStr := dateFrom.Format("2006-01-02")
	toStr := dateTo.Format("2006-01-02")
	var notices []groupOOONotice
	for _, host := range hosts {
		rows, err := h.db.QueryContext(ctx, `
			SELECT o.dates_json, o.reason, o.public_note, COALESCE(ru.handle, ''), COALESCE(ru.name, '')
			FROM ooo_entries o
			LEFT JOIN users ru ON ru.id = o.redirect_user_id
			WHERE o.user_id = ?
			   OR (o.team_id IS NOT NULL AND o.team_id IN (SELECT team_id FROM team_members WHERE user_id = ?))`,
			host.ID, host.ID)
		if err != nil {
			continue
		}
		for rows.Next() {
			var datesRaw, reason, note, redirHandle, redirName string
			if err := rows.Scan(&datesRaw, &reason, &note, &redirHandle, &redirName); err != nil {
				continue
			}
			var dates []string
			if err := json.Unmarshal([]byte(datesRaw), &dates); err != nil {
				continue
			}
			inRange := false
			for _, d := range dates {
				if d >= fromStr && d <= toStr {
					inRange = true
					break
				}
			}
			if inRange && (note != "" || redirHandle != "") {
				notices = append(notices, groupOOONotice{
					HostName:       host.Name,
					Reason:         reason,
					PublicNote:     note,
					RedirectHandle: redirHandle,
					RedirectName:   redirName,
				})
			}
		}
		rows.Close() // #nosec G104
	}
	return notices
}

func (h *Handler) computeGroupSlots(ctx context.Context, hosts []groupHostInfo, dateFrom, dateTo time.Time, durMin int, bookerLoc *time.Location) ([]slots.Slot, error) {
	hostAvails := make([]slots.HostAvailability, 0, len(hosts))
	for _, host := range hosts {
		ha, _, err := h.hostAvailability(ctx, host.ID, "", dateFrom, dateTo)
		if err != nil {
			return nil, err
		}
		hostAvails = append(hostAvails, ha)
	}
	return slots.Generate(slots.Request{
		Event: slots.EventConfig{
			DurationMinutes:     durMin,
			SlotIntervalMinutes: durMin,
			MaxFutureDays:       60,
			RoutingMode:         "collective",
		},
		Hosts:    hostAvails,
		DateFrom: dateFrom,
		DateTo:   dateTo,
		BookerTZ: bookerLoc,
		Now:      time.Now().UTC(),
	})
}

// GetDynamicGroupSlots handles GET /v1/public/group/{handles}/slots.
func (h *Handler) GetDynamicGroupSlots(w http.ResponseWriter, r *http.Request) {
	hosts, err := h.resolveGroupHosts(r.Context(), r.PathValue("handles"))
	if err == sql.ErrNoRows {
		h.writeError(w, http.StatusNotFound, "one or more handles not found")
		return
	}
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	now := time.Now().UTC()
	fromStr := r.URL.Query().Get("from")
	toStr := r.URL.Query().Get("to")
	dateFrom := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if fromStr != "" {
		if parsed, err := time.Parse("2006-01-02", fromStr); err == nil {
			dateFrom = parsed
		}
	}
	dateTo := dateFrom.AddDate(0, 0, 14)
	if toStr != "" {
		if parsed, err := time.Parse("2006-01-02", toStr); err == nil && !parsed.Before(dateFrom) {
			dateTo = parsed
		}
	}
	if dateTo.Sub(dateFrom) > 60*24*time.Hour {
		dateTo = dateFrom.AddDate(0, 0, 60)
	}

	durMin := 30
	if rawDur := r.URL.Query().Get("duration"); rawDur != "" {
		if d, err := strconv.Atoi(rawDur); err == nil && d >= 15 && d <= 180 {
			durMin = d
		}
	}

	tzName := strings.TrimSpace(r.URL.Query().Get("tz"))
	if tzName == "" {
		tzName = "UTC"
	}
	bookerLoc, err := time.LoadLocation(tzName)
	if err != nil {
		bookerLoc = time.UTC
	}

	availSlots, err := h.computeGroupSlots(r.Context(), hosts, dateFrom, dateTo, durMin, bookerLoc)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "compute group slots", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if availSlots == nil {
		availSlots = []slots.Slot{}
	}

	notices := h.groupOOONotices(r.Context(), hosts, dateFrom, dateTo)
	h.writeJSON(w, http.StatusOK, map[string]any{
		"hosts":            hosts,
		"duration_minutes": durMin,
		"slots":            availSlots,
		"ooo_notices":      notices,
	})
}

// CreateDynamicGroupBooking handles POST /v1/public/group/{handles}/bookings.
func (h *Handler) CreateDynamicGroupBooking(w http.ResponseWriter, r *http.Request) {
	hosts, err := h.resolveGroupHosts(r.Context(), r.PathValue("handles"))
	if err == sql.ErrNoRows {
		h.writeError(w, http.StatusNotFound, "one or more handles not found")
		return
	}
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	var req struct {
		StartAt         string `json:"start_at"`
		DurationMinutes int    `json:"duration_minutes"`
		AttendeeName    string `json:"attendee_name"`
		AttendeeEmail   string `json:"attendee_email"`
		AttendeeTZ      string `json:"attendee_tz"`
		Notes           string `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.AttendeeName = strings.TrimSpace(req.AttendeeName)
	req.AttendeeEmail = strings.TrimSpace(req.AttendeeEmail)
	if req.AttendeeName == "" || req.AttendeeEmail == "" || !strings.Contains(req.AttendeeEmail, "@") {
		h.writeError(w, http.StatusBadRequest, "attendee_name and valid attendee_email are required")
		return
	}
	if req.DurationMinutes < 15 || req.DurationMinutes > 180 {
		req.DurationMinutes = 30
	}
	startAt, err := time.Parse(time.RFC3339, req.StartAt)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "start_at must be RFC3339")
		return
	}
	startUTC := startAt.UTC()
	endUTC := startUTC.Add(time.Duration(req.DurationMinutes) * time.Minute)
	if req.AttendeeTZ == "" {
		req.AttendeeTZ = "UTC"
	}

	day := time.Date(startUTC.Year(), startUTC.Month(), startUTC.Day(), 0, 0, 0, 0, time.UTC)
	availSlots, err := h.computeGroupSlots(r.Context(), hosts, day.AddDate(0, 0, -1), day.AddDate(0, 0, 1), req.DurationMinutes, time.UTC)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	slotOK := false
	for _, s := range availSlots {
		if s.Start.UTC().Equal(startUTC) {
			slotOK = true
			break
		}
	}
	if !slotOK {
		h.writeError(w, http.StatusConflict, "selected time slot is no longer available for all hosts")
		return
	}

	// Ensure primary host has an internal hidden group event type so bookings.event_type_id FK is satisfied.
	primaryHost := hosts[0]
	internalSlug := "__group__" + primaryHost.ID
	var etID string
	err = h.db.QueryRowContext(r.Context(), `SELECT id FROM event_types WHERE slug = ?`, internalSlug).Scan(&etID)
	if err == sql.ErrNoRows {
		etID = uid.New()
		_, err = h.db.ExecContext(r.Context(), `
			INSERT INTO event_types (id, user_id, slug, name, duration_minutes, slot_interval_minutes, location_type, is_active, is_public, routing_mode)
			VALUES (?, ?, ?, 'Group Meeting', 30, 30, 'link', 0, 0, 'collective')`,
			etID, primaryHost.ID, internalSlug)
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "ensure group event type", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	bookingID := uid.New()
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback() // #nosec G104

	nowStr := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(r.Context(), `
		INSERT INTO bookings (id, event_type_id, host_id, start_at, end_at, status, location_type, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 'confirmed', 'link', ?, ?)`,
		bookingID, etID, primaryHost.ID,
		startUTC.Format(time.RFC3339), endUTC.Format(time.RFC3339),
		nowStr, nowStr); err != nil {
		h.logger.ErrorContext(r.Context(), "insert group booking", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	for i, host := range hosts {
		isPrimary := 0
		if i == 0 {
			isPrimary = 1
		}
		if _, err := tx.ExecContext(r.Context(), `
			INSERT INTO booking_hosts (id, booking_id, user_id, is_primary) VALUES (?, ?, ?, ?)`,
			uid.New(), bookingID, host.ID, isPrimary); err != nil {
			h.logger.ErrorContext(r.Context(), "insert group booking host", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}
	if _, err := tx.ExecContext(r.Context(), `
		INSERT INTO booking_attendees (id, booking_id, name, email, iana_timezone, is_organizer, locale)
		VALUES (?, ?, ?, ?, ?, 1, 'en')`,
		uid.New(), bookingID, req.AttendeeName, req.AttendeeEmail, req.AttendeeTZ); err != nil {
		h.logger.ErrorContext(r.Context(), "insert group booking attendee", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := tx.Commit(); err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.writeJSON(w, http.StatusCreated, map[string]any{
		"id":               bookingID,
		"status":           "confirmed",
		"start_at":         startUTC.Format(time.RFC3339),
		"end_at":           endUTC.Format(time.RFC3339),
		"duration_minutes": req.DurationMinutes,
		"hosts":            hosts,
	})
}

var groupPageTmpl = template.Must(template.New("group_book").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8" />
<meta name="viewport" content="width=device-width,initial-scale=1" />
<title>Group Booking — {{.Names}}</title>
<style>
  body { font-family: system-ui, -apple-system, sans-serif; background: #0b0d10; color: #f3f4f6; margin: 0; padding: 2rem 1rem; }
  .card { max-width: 680px; margin: 0 auto; background: #14181f; border: 1px solid #262c36; border-radius: 12px; padding: 1.5rem; }
  h1 { margin: 0 0 .25rem; font-size: 1.25rem; }
  .muted { color: #9ca3af; font-size: .875rem; }
  .row { display: flex; gap: .75rem; flex-wrap: wrap; margin: 1rem 0; }
  select, input, textarea, button { font: inherit; border-radius: 8px; border: 1px solid #303744; background: #0f1318; color: #f3f4f6; padding: .5rem .75rem; }
  button { background: #2563eb; border-color: #2563eb; cursor: pointer; font-weight: 500; }
  button:disabled { opacity: .5; cursor: not-allowed; }
  .slots { display: grid; grid-template-columns: repeat(auto-fill, minmax(120px, 1fr)); gap: .5rem; margin: 1rem 0; }
  .slot { background: #1c222d; border: 1px solid #303744; text-align: center; }
  .slot.selected { background: #2563eb; border-color: #2563eb; }
  .ooo { background: #2a1f0f; border: 1px solid #78350f; border-radius: 8px; padding: .75rem 1rem; margin: 1rem 0; font-size: .875rem; }
  .ok { background: #064e3b; border: 1px solid #10b981; border-radius: 8px; padding: 1rem; margin-top: 1rem; }
</style>
</head>
<body>
<div class="card">
  <h1>Group Meeting</h1>
  <p class="muted">With {{.Names}} — showing times when everyone is available.</p>
  <div id="ooo"></div>
  <div class="row">
    <label>Duration
      <select id="dur">
        <option value="15">15 min</option>
        <option value="30" selected>30 min</option>
        <option value="45">45 min</option>
        <option value="60">60 min</option>
      </select>
    </label>
    <label>Date
      <input id="date" type="date" />
    </label>
  </div>
  <div id="slots" class="slots"></div>
  <form id="bookForm" style="display:none; flex-direction:column; gap:.75rem; margin-top:1rem;">
    <input id="name" placeholder="Your name" required />
    <input id="email" type="email" placeholder="you@example.com" required />
    <textarea id="notes" rows="2" placeholder="Notes (optional)"></textarea>
    <button type="submit">Confirm Group Booking</button>
  </form>
  <div id="result"></div>
</div>
<script>
const handles = {{.HandlesJSON}};
const tz = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
const dateEl = document.getElementById('date');
const durEl = document.getElementById('dur');
const slotsEl = document.getElementById('slots');
const oooEl = document.getElementById('ooo');
const formEl = document.getElementById('bookForm');
const resEl = document.getElementById('result');
let selectedStart = '';

dateEl.value = new Date().toISOString().slice(0, 10);

async function loadSlots() {
  selectedStart = '';
  formEl.style.display = 'none';
  slotsEl.innerHTML = '<span class="muted">Loading...</span>';
  const d = dateEl.value;
  const res = await fetch('/v1/public/group/' + encodeURIComponent(handles) + '/slots?from=' + d + '&to=' + d + '&duration=' + durEl.value + '&tz=' + encodeURIComponent(tz));
  const data = await res.json();
  oooEl.innerHTML = '';
  (data.ooo_notices || []).forEach(n => {
    const div = document.createElement('div');
    div.className = 'ooo';
    const strong = document.createElement('strong');
    strong.textContent = n.host_name;
    div.appendChild(strong);
    div.appendChild(document.createTextNode(' (' + n.reason + '): ' + (n.public_note || '')));
    if (n.redirect_handle) {
      div.appendChild(document.createTextNode(' — '));
      const a = document.createElement('a');
      a.style.color = '#93c5fd';
      a.href = '/u/' + encodeURIComponent(n.redirect_handle);
      a.textContent = 'Book with ' + (n.redirect_name || n.redirect_handle);
      div.appendChild(a);
    }
    oooEl.appendChild(div);
  });
  if (!data.slots || data.slots.length === 0) {
    slotsEl.innerHTML = '<span class="muted">No shared slots on this date.</span>';
    return;
  }
  slotsEl.innerHTML = '';
  data.slots.forEach(s => {
    const b = document.createElement('button');
    b.type = 'button';
    b.className = 'slot';
    b.textContent = new Date(s.start).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', timeZone: tz });
    b.onclick = () => {
      document.querySelectorAll('.slot').forEach(x => x.classList.remove('selected'));
      b.classList.add('selected');
      selectedStart = s.start;
      formEl.style.display = 'flex';
    };
    slotsEl.appendChild(b);
  });
}
dateEl.onchange = loadSlots;
durEl.onchange = loadSlots;
formEl.onsubmit = async (e) => {
  e.preventDefault();
  const r = await fetch('/v1/public/group/' + encodeURIComponent(handles) + '/bookings', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      start_at: selectedStart,
      duration_minutes: Number(durEl.value),
      attendee_name: document.getElementById('name').value,
      attendee_email: document.getElementById('email').value,
      attendee_tz: tz,
      notes: document.getElementById('notes').value
    })
  });
  const out = await r.json();
  if (r.ok) {
    formEl.style.display = 'none';
    resEl.innerHTML = '<div class="ok">Confirmed for ' + new Date(out.start_at).toLocaleString([], { timeZone: tz }) + '</div>';
  } else {
    resEl.innerHTML = '<p style="color:#f87171">' + (out.error || 'Booking failed') + '</p>';
  }
};
loadSlots();
</script>
</body>
</html>`))

// DynamicGroupBookPage handles GET /u/{handle} when handle contains '+'.
func (h *Handler) DynamicGroupBookPage(w http.ResponseWriter, r *http.Request) {
	raw := r.PathValue("handle")
	if raw == "" {
		raw = r.PathValue("handles")
	}
	hosts, err := h.resolveGroupHosts(r.Context(), raw)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	names := make([]string, 0, len(hosts))
	for _, host := range hosts {
		names = append(names, host.Name)
	}
	handlesJSON, _ := json.Marshal(raw)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = groupPageTmpl.Execute(w, map[string]any{
		"Names":       strings.Join(names, " + "),
		"HandlesJSON": template.JS(handlesJSON), // #nosec G203 -- json.Marshal escapes HTML/script chars
	})
}
