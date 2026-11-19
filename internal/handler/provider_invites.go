package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/access"
	"github.com/calnode/calnode/internal/uid"
)

// The invitation, granted role, account and immutable identity are committed in
// the caller's single transaction. An invite never disables instance SSO policy.
func (h *Handler) claimProviderInvite(ctx context.Context, tx *sql.Tx, hash, email, name, providerID string) (string, error) {
	var inviteID, invitedEmail, role, roleID, issuer string
	now := time.Now().UTC().Format(time.RFC3339)
	if tx.QueryRowContext(ctx, `SELECT id,email,role,COALESCE(custom_role_id,''),created_by FROM invite_tokens WHERE token_hash=? AND used_at IS NULL AND expires_at>?`, hash, now).Scan(&inviteID, &invitedEmail, &role, &roleID, &issuer) != nil {
		return "", fmt.Errorf("invitation expired")
	}
	if !strings.EqualFold(email, invitedEmail) {
		return "", fmt.Errorf("sign in with the verified email address this invitation was sent to")
	}
	grants, err := access.Load(ctx, tx, issuer)
	required := "members.invite_member"
	if role == "admin" {
		required = "members.invite_admin"
	}
	if err != nil || !grants.Has(required) {
		return "", fmt.Errorf("invitation is no longer authorized")
	}
	if roleID != "" {
		requested, e := access.Role(ctx, tx, roleID)
		if role == "admin" || e != nil || access.Validate(requested, grants, false) != nil {
			return "", fmt.Errorf("invited role is no longer authorized")
		}
	}
	id := uid.New()
	if name == "" {
		name = invitedEmail
	}
	admin := role == "admin"
	if _, err = tx.ExecContext(ctx, `INSERT INTO users(id,email,name,iana_timezone,is_admin,email_login,custom_role_id,enrollment_source)VALUES(?,?,?,'UTC',?,0,NULLIF(?,''),'invite')`, id, invitedEmail, name, admin, roleID); err != nil {
		return "", fmt.Errorf("this email already has an account; link the provider from that account")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE invite_tokens SET used_at=? WHERE id=? AND used_at IS NULL`, now, inviteID); err != nil {
		return "", fmt.Errorf("could not accept invitation")
	}
	details, _ := json.Marshal(map[string]string{"role": role, "provider_id": providerID})
	if _, err = tx.ExecContext(ctx, `INSERT INTO security_audit_logs(id,actor_user_id,action,target_id,details)VALUES(?,?,'invite.claim',?,?)`, uid.New(), issuer, id, string(details)); err != nil {
		return "", fmt.Errorf("could not audit invitation acceptance")
	}
	return id, nil
}

func (h *Handler) ListMyIdentities(w http.ResponseWriter, r *http.Request) {
	u, _ := userFromContext(r.Context())
	var enrolled string
	if h.db.QueryRowContext(r.Context(), `SELECT enrollment_provider_id FROM users WHERE id=?`, u.ID).Scan(&enrolled) != nil {
		h.writeError(w, 500, "could not load identities")
		return
	}
	rows, err := h.db.QueryContext(r.Context(), `SELECT p.id,p.name,p.kind,EXISTS(SELECT 1 FROM user_identities i WHERE i.provider_id=p.id AND i.user_id=?) FROM identity_providers p WHERE enabled=1 AND purpose IN('sso','both') AND (?=1 OR allow_members=1) AND (?='' OR id=?) ORDER BY name`, u.ID, u.InstanceAdmin || u.InstanceOwner, enrolled, enrolled)
	if err != nil {
		h.writeError(w, 500, "could not load identities")
		return
	}
	defer rows.Close()
	type item struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Kind   string `json:"kind"`
		Linked bool   `json:"linked"`
	}
	items := []item{}
	for rows.Next() {
		var i item
		if rows.Scan(&i.ID, &i.Name, &i.Kind, &i.Linked) != nil {
			h.writeError(w, 500, "could not load identities")
			return
		}
		items = append(items, i)
	}
	if rows.Err() != nil {
		h.writeError(w, 500, "could not load identities")
		return
	}
	h.writeJSON(w, 200, map[string]any{"items": items})
}
