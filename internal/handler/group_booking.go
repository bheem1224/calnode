package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/slots"
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
		// Reserved references remain valid if a public handle is added later.
		byID := strings.HasPrefix(handle, "id:")
		identifier := strings.TrimPrefix(handle, "id:")
		err := h.db.QueryRowContext(ctx, `
			SELECT id, name, COALESCE(handle, ''), iana_timezone
			FROM users
			WHERE archived_at IS NULL AND ((?=1 AND LOWER(id)=?) OR (?=0 AND LOWER(handle)=?))`, boolArg(byID), identifier, boolArg(byID), identifier).
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
	h.writeError(w, http.StatusGone, "create a new single-use dynamic link from the scheduling app")
}

// CreateDynamicGroupBooking handles POST /v1/public/group/{handles}/bookings.
func (h *Handler) CreateDynamicGroupBooking(w http.ResponseWriter, r *http.Request) {
	h.writeError(w, http.StatusGone, "create a new single-use dynamic link from the scheduling app")
}

// DynamicGroupBookPage handles GET /u/{handle} when handle contains '+'.
func (h *Handler) DynamicGroupBookPage(w http.ResponseWriter, r *http.Request) {
	h.writeError(w, http.StatusGone, "create a new single-use dynamic link from the scheduling app")
}
