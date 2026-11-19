package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/calnode/calnode/internal/access"
	"github.com/calnode/calnode/internal/connstore"
	"github.com/calnode/calnode/internal/uid"
	"net/http"
	"slices"
	"strings"
	"time"
)

func (h *Handler) CreateDynamicLink(w http.ResponseWriter, r *http.Request) {
	u, _ := userFromContext(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	var in struct {
		HostIDs       []string `json:"host_ids"`
		Duration      int      `json:"duration_minutes"`
		Organizer     string   `json:"organizer_id"`
		Name          string   `json:"name"`
		Durations     []int    `json:"duration_options"`
		LocationType  string   `json:"location_type"`
		LocationValue string   `json:"location_value"`
		DestinationID string   `json:"destination_id"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		h.writeError(w, 400, "invalid JSON")
		return
	}
	if in.Duration == 0 {
		in.Duration = 30
	}
	if in.Duration < 15 || in.Duration > 180 {
		h.writeError(w, 400, "duration must be between 15 and 180 minutes")
		return
	}
	if !h.dynamicHostsAllowed(r.Context(), u, in.HostIDs) {
		h.writeError(w, 403, "your role does not permit scheduling this group")
		return
	}
	if in.Organizer == "" {
		in.Organizer = in.HostIDs[0]
	}
	if !slices.Contains(in.HostIDs, in.Organizer) {
		h.writeError(w, 400, "organizer must be a selected host")
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		in.Name = "Group meeting"
	}
	if len([]rune(in.Name)) > 120 {
		h.writeError(w, 400, "meeting title is too long")
		return
	}
	if len(in.Durations) == 0 {
		in.Durations = []int{in.Duration}
	}
	if len(in.Durations) > 8 || !slices.Contains(in.Durations, in.Duration) {
		h.writeError(w, 400, "choose up to eight durations including the default")
		return
	}
	seen := map[int]bool{}
	for _, d := range in.Durations {
		if d < 15 || d > 180 || seen[d] {
			h.writeError(w, 400, "durations must be unique and between 15 and 180 minutes")
			return
		}
		seen[d] = true
	}
	dest := ""
	if in.DestinationID != "" {
		cals, err := h.meetingCalendars(r.Context(), u.ID, in.Organizer)
		if err != nil {
			h.writeError(w, 503, err.Error())
			return
		}
		if !slices.ContainsFunc(cals, func(c meetingCalendar) bool { return c.ID == in.DestinationID }) {
			h.writeError(w, 400, "select a writable calendar belonging to the organizer")
			return
		}
		var provider, calendarID, account string
		if h.db.QueryRowContext(r.Context(), `SELECT provider,calendar_id,account_email FROM connection_calendars WHERE id=? AND user_id=?`, in.DestinationID, in.Organizer).Scan(&provider, &calendarID, &account) != nil {
			h.writeError(w, 400, "calendar selection changed; try again")
			return
		}
		dest = connstore.CalendarReference(provider, calendarID, account)
	}
	if in.LocationType == "" {
		in.LocationType = h.smartDefaultLocation(r.Context(), in.Organizer)
	}
	in.LocationValue = strings.TrimSpace(in.LocationValue)
	if len(in.LocationValue) > 2048 {
		h.writeError(w, 400, "location is too long")
		return
	}
	if err := h.meetingLocationValid(r.Context(), in.Organizer, in.LocationType, in.LocationValue, dest); err != nil {
		h.writeError(w, 400, err.Error())
		return
	}
	durations, _ := json.Marshal(in.Durations)

	raw, err := randomAuthToken()
	if err != nil {
		h.writeError(w, 500, "could not create link")
		return
	}
	hosts, _ := json.Marshal(in.HostIDs)
	eventID, tokenID := uid.New(), uid.New()
	now := time.Now().UTC()
	p, err := h.signinPolicy(r.Context())
	if err != nil {
		h.writeError(w, 503, "security settings unavailable")
		return
	}
	if p.DynamicLinkDays <= 0 {
		p.DynamicLinkDays = 7
	}
	expires := now.Add(time.Duration(p.DynamicLinkDays) * 24 * time.Hour).Format(time.RFC3339)
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.writeError(w, 500, "could not create link")
		return
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err = tx.ExecContext(r.Context(), `INSERT INTO event_types(id,user_id,slug,name,duration_minutes,slot_interval_minutes,routing_mode,is_active,is_public,location_type,location_value,allow_multiple_durations,duration_options,destination_calendar_id)VALUES(?,?,?,?,?,?,'collective',1,0,?,?,?,?,?)`, eventID, in.Organizer, "__dynamic__"+eventID, in.Name, in.Duration, in.Duration, in.LocationType, in.LocationValue, len(in.Durations) > 1, string(durations), dest); err == nil {
		for index, host := range in.HostIDs {
			priority := index + 1
			if host == in.Organizer {
				priority = 0
			}
			_, err = tx.ExecContext(r.Context(), `INSERT INTO event_type_hosts(id,event_type_id,user_id,role,priority)VALUES(?,?,?,'required',?)`, uid.New(), eventID, host, priority)
			if err != nil {
				break
			}
		}
	}
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO single_use_tokens(id,token,event_type_id,created_by_user_id,expires_at,created_at)VALUES(?,?,?,?,?,?)`, tokenID, raw, eventID, u.ID, expires, now.Format(time.RFC3339))
	}
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO dynamic_links(token_id,creator_id,host_ids_json)VALUES(?,?,?)`, tokenID, u.ID, string(hosts))
	}
	if err != nil || tx.Commit() != nil {
		h.writeError(w, 500, "could not create link")
		return
	}
	h.securityAudit(r.Context(), u.ID, "dynamic_link.create", tokenID, map[string]any{"host_ids": in.HostIDs, "expires_at": expires})
	h.writeJSON(w, 201, map[string]any{"organizer_id": in.Organizer, "url": strings.TrimRight(h.publicURL(), "/") + "/t/" + raw, "expires_at": expires})
}

func (h *Handler) dynamicLinkAllowed(ctx context.Context, token string) bool {
	var creator, raw, host, location, value, destination string
	err := h.db.QueryRowContext(ctx, `SELECT d.creator_id,d.host_ids_json,e.user_id,e.location_type,COALESCE(e.location_value,''),COALESCE(e.destination_calendar_id,'') FROM dynamic_links d JOIN single_use_tokens t ON t.id=d.token_id JOIN event_types e ON e.id=t.event_type_id WHERE t.token=?`, token).Scan(&creator, &raw, &host, &location, &value, &destination)
	if err == sql.ErrNoRows {
		return true
	}
	if err != nil {
		return false
	}
	var hosts []string
	g, err := access.Load(ctx, h.db, creator)
	return err == nil && h.meetingLocationValid(ctx, host, location, value, destination) == nil && json.Unmarshal([]byte(raw), &hosts) == nil && access.DynamicHostsAllowed(ctx, h.db, creator, g["dynamic_links.create"], hosts)
}

func (h *Handler) dynamicHostsAllowed(ctx context.Context, u AuthUser, hosts []string) bool {
	return access.DynamicHostsAllowed(ctx, h.db, u.ID, u.Permissions["dynamic_links.create"], hosts)
}
