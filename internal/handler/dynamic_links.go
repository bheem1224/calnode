package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/calnode/calnode/internal/access"
	"github.com/calnode/calnode/internal/uid"
	"net/http"
	"strings"
	"time"
)

func (h *Handler) CreateDynamicLink(w http.ResponseWriter, r *http.Request) {
	u, _ := userFromContext(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	var in struct {
		HostIDs  []string `json:"host_ids"`
		Duration int      `json:"duration_minutes"`
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
	if !access.DynamicHostsAllowed(r.Context(), h.db, u.ID, u.Permissions["dynamic_links.create"], in.HostIDs) {
		h.writeError(w, 403, "your role does not permit scheduling this group")
		return
	}
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
	if _, err = tx.ExecContext(r.Context(), `INSERT INTO event_types(id,user_id,slug,name,duration_minutes,slot_interval_minutes,routing_mode,is_active,is_public,location_type,allow_multiple_durations,duration_options)VALUES(?,?,?,'Group meeting',?,?,'collective',1,0,'link',1,'[15,20,30,45,60,90,120,180]')`, eventID, u.ID, "__dynamic__"+eventID, in.Duration, in.Duration); err == nil {
		for _, host := range in.HostIDs {
			_, err = tx.ExecContext(r.Context(), `INSERT INTO event_type_hosts(id,event_type_id,user_id,role)VALUES(?,?,?,'required')`, uid.New(), eventID, host)
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
	h.writeJSON(w, 201, map[string]any{"url": strings.TrimRight(h.publicURL(), "/") + "/t/" + raw, "expires_at": expires})
}

func (h *Handler) dynamicLinkAllowed(ctx context.Context, token string) bool {
	var creator, raw string
	err := h.db.QueryRowContext(ctx, `SELECT d.creator_id,d.host_ids_json FROM dynamic_links d JOIN single_use_tokens t ON t.id=d.token_id WHERE t.token=?`, token).Scan(&creator, &raw)
	if err == sql.ErrNoRows {
		return true
	}
	if err != nil {
		return false
	}
	var hosts []string
	g, err := access.Load(ctx, h.db, creator)
	return err == nil && json.Unmarshal([]byte(raw), &hosts) == nil && access.DynamicHostsAllowed(ctx, h.db, creator, g["dynamic_links.create"], hosts)
}
