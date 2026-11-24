package handler

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

type adminAlertOption struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Owner bool   `json:"is_owner"`
}

func (h *Handler) adminAlertOptions(ctx context.Context) ([]adminAlertOption, error) {
	rows, err := h.db.QueryContext(ctx, `SELECT id,name,email,is_owner FROM users WHERE archived_at IS NULL AND (is_admin=1 OR is_owner=1) ORDER BY name,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	options := []adminAlertOption{}
	for rows.Next() {
		var o adminAlertOption
		if err := rows.Scan(&o.ID, &o.Name, &o.Email, &o.Owner); err != nil {
			return nil, err
		}
		options = append(options, o)
	}
	return options, rows.Err()
}

func (h *Handler) validateAdminAlerts(ctx context.Context, p *SigninPolicy) error {
	if p.AdminAlertMode == "" {
		p.AdminAlertMode = "owner"
	}
	if p.AdminAlertMode != "owner" && p.AdminAlertMode != "admins" && p.AdminAlertMode != "selected" {
		return fmt.Errorf("choose owner, all administrators, or selected administrators for alerts")
	}
	if len(p.AdminAlertUserIDs) > 50 {
		return fmt.Errorf("select at most 50 alert recipients")
	}
	if p.AdminAlertMode != "selected" {
		p.AdminAlertUserIDs = []string{}
		return nil
	}
	options, err := h.adminAlertOptions(ctx)
	if err != nil {
		return fmt.Errorf("could not validate alert recipients")
	}
	allowed := map[string]bool{}
	for _, o := range options {
		allowed[o.ID] = true
	}
	seen := map[string]bool{}
	ids := []string{}
	for _, id := range p.AdminAlertUserIDs {
		if !allowed[id] {
			return fmt.Errorf("alert recipients must be active instance administrators")
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return fmt.Errorf("select at least one administrator for alerts")
	}
	sort.Strings(ids)
	p.AdminAlertUserIDs = ids
	return nil
}

func (h *Handler) securityNoticeEmails(ctx context.Context, affected string) ([]string, error) {
	p, err := h.signinPolicy(ctx)
	if err != nil {
		return nil, err
	}
	options, err := h.adminAlertOptions(ctx)
	if err != nil {
		return nil, err
	}
	selected := map[string]bool{}
	for _, id := range p.AdminAlertUserIDs {
		selected[id] = true
	}
	emails := map[string]bool{}
	haveOwner := false
	for _, o := range options {
		haveOwner = haveOwner || o.Owner
	}
	for _, o := range options {
		include := o.Owner || !haveOwner
		if p.AdminAlertMode == "admins" {
			include = true
		} else if p.AdminAlertMode == "selected" {
			include = selected[o.ID]
		}
		if include {
			emails[strings.ToLower(o.Email)] = true
		}
	}
	// If selected recipients were archived/demoted, keep alerts with the owner.
	// Older installations without an owner retain their administrative fallback.
	if len(emails) == 0 {
		for _, o := range options {
			if o.Owner || !haveOwner {
				emails[strings.ToLower(o.Email)] = true
			}
		}
	}
	var email string
	err = h.db.QueryRowContext(ctx, `SELECT email FROM users WHERE id=? AND archived_at IS NULL`, affected).Scan(&email)
	if err == nil {
		emails[strings.ToLower(email)] = true
	}
	// A deleted affected account must not suppress the administrator notice.
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	out := []string{}
	for email := range emails {
		out = append(out, email)
	}
	sort.Strings(out)
	return out, nil
}
