package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/calnode/calnode/internal/connstore"
	"net/http"
	"slices"
	"strings"
)

type meetingCalendar struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Provider string `json:"provider"`
}

// Expose opaque saved-selection IDs, never another host's account addresses or
// private calendar names. Overrides are checked against live writable calendars.
func (h *Handler) meetingCalendars(ctx context.Context, actor, host string) ([]meetingCalendar, error) {
	out := []meetingCalendar{}
	gc := h.getCal()
	if gc == nil {
		return out, nil
	}
	connections, err := gc.Connections(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, conn := range connections {
		if conn.Provider != "google" && conn.Provider != "microsoft" {
			continue
		}
		cals, err := gc.AccountCalendars(ctx, host, conn.Provider, conn.AccountEmail)
		if err != nil {
			return nil, fmt.Errorf("calendar information unavailable; reconnect the organizer's calendar")
		}
		for _, cal := range cals {
			if !cal.Writable {
				continue
			}
			var id string
			if h.db.QueryRowContext(ctx, `SELECT id FROM connection_calendars WHERE user_id=? AND provider=? AND account_email=? AND calendar_id=?`, host, conn.Provider, conn.AccountEmail, cal.ID).Scan(&id) != nil {
				continue
			}
			label := fmt.Sprintf("%s · %d", conn.Provider, len(out)+1)
			if actor == host {
				label = cal.Name
			}
			out = append(out, meetingCalendar{ID: id, Label: label, Provider: conn.Provider})
		}
	}
	return out, nil
}

func (h *Handler) meetingLocationValid(ctx context.Context, host, kind, value, dest string) error {
	if kind == "google_meet" || kind == "teams" {
		if value == "" && dest != "" {
			accountKind := ""
			provider, _, email, err := connstore.CalendarTarget(ctx, h.db, host, dest)
			if err == nil {
				err = h.db.QueryRowContext(ctx, `SELECT COALESCE(account_kind,'') FROM calendar_connections WHERE user_id=? AND provider=? AND COALESCE(account_email,'')=?`, host, provider, email).Scan(&accountKind)
			}

			if err != nil || !providerMintsPlatform(kind, provider) || (kind == "teams" && accountKind == "personal") {
				return fmt.Errorf("the selected calendar cannot generate this meeting type; select a compatible calendar or enter a meeting link")
			}
			return nil
		}
	}
	if !slices.Contains([]string{"google_meet", "teams", "zoom", "livekit", "link", "in_person", "phone", "attendee_phone"}, kind) {
		return fmt.Errorf("invalid meeting location")
	}
	return h.validateLocation(ctx, host, kind, &value)
}

func (h *Handler) GetMeetingDefaults(w http.ResponseWriter, r *http.Request) {
	u, _ := userFromContext(r.Context())
	var kind string
	if h.db.QueryRowContext(r.Context(), `SELECT default_conferencing_type FROM users WHERE id=?`, u.ID).Scan(&kind) != nil {
		h.writeError(w, 500, "could not load meeting defaults")
		return
	}
	choices := []string{}
	for _, k := range []string{"google_meet", "teams", "zoom", "livekit"} {
		if h.meetingLocationValid(r.Context(), u.ID, k, "", "") == nil {
			choices = append(choices, k)
		}
	}
	h.writeJSON(w, 200, map[string]any{"conferencing_type": kind, "available_conferencing": choices})
}

func (h *Handler) PutMeetingDefaults(w http.ResponseWriter, r *http.Request) {
	u, _ := userFromContext(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	var in struct {
		Kind string `json:"conferencing_type"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		h.writeError(w, 400, "invalid JSON")
		return
	}
	if in.Kind != "" {
		if !slices.Contains([]string{"google_meet", "teams", "zoom", "livekit"}, in.Kind) {
			h.writeError(w, 400, "choose a connected conferencing provider")
			return
		}
		if err := h.meetingLocationValid(r.Context(), u.ID, in.Kind, "", ""); err != nil {
			h.writeError(w, 400, err.Error())
			return
		}
	}
	if _, err := h.db.ExecContext(r.Context(), `UPDATE users SET default_conferencing_type=? WHERE id=?`, in.Kind, u.ID); err != nil {
		h.writeError(w, 500, "could not save meeting defaults")
		return
	}
	h.GetMeetingDefaults(w, r)
}

func (h *Handler) DynamicLinkOptions(w http.ResponseWriter, r *http.Request) {
	u, _ := userFromContext(r.Context())
	hosts := strings.Split(r.URL.Query().Get("host_ids"), ",")
	if !h.dynamicHostsAllowed(r.Context(), u, hosts) {
		h.writeError(w, 403, "your role does not permit scheduling this group")
		return
	}
	host := r.URL.Query().Get("organizer_id")
	if !slices.Contains(hosts, host) {
		h.writeError(w, 400, "organizer must be a selected host")
		return
	}
	cals, err := h.meetingCalendars(r.Context(), u.ID, host)
	if err != nil {
		h.writeError(w, 503, err.Error())
		return
	}
	h.writeJSON(w, 200, map[string]any{"location_type": h.smartDefaultLocation(r.Context(), host), "calendars": cals})
}
