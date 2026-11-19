// Package identityprovider stores OAuth application configuration independently
// from user identities and calendar grants. Secrets never appear in JSON.
package identityprovider

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/calnode/calnode/internal/secret"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"golang.org/x/oauth2/microsoft"
)

type Provider struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Kind              string   `json:"kind"`
	Purpose           string   `json:"purpose"`
	Enabled           bool     `json:"enabled"`
	AllowMembers      bool     `json:"allow_members"`
	ClientID          string   `json:"client_id"`
	SecretEnc         string   `json:"-"`
	SecretSet         bool     `json:"client_secret_set"`
	MicrosoftTenantID string   `json:"microsoft_tenant_id"`
	IssuerURL         string   `json:"issuer_url"`
	PKCE              bool     `json:"pkce"`
	AutoProvision     bool     `json:"auto_provision"`
	Domains           []string `json:"approved_domains"`
	GroupIDs          []string `json:"group_ids"`
	GroupMatch        string   `json:"group_match"`
	GroupsClaim       string   `json:"groups_claim"`
	LegacyKey         string   `json:"legacy_key"`
	Revision          int      `json:"revision"`
}

func (p Provider) SSO() bool { return p.Enabled && (p.Purpose == "sso" || p.Purpose == "both") }
func (p Provider) Integrations() bool {
	return p.Enabled && p.Kind != "oidc" && (p.Purpose == "integrations" || p.Purpose == "both")
}

const Columns = `id,name,kind,purpose,enabled,allow_members,client_id,client_secret_enc,microsoft_tenant_id,issuer_url,pkce,auto_provision,domains_json,group_ids_json,group_match,groups_claim,legacy_key,revision`

type Scanner interface{ Scan(...any) error }

func Scan(row Scanner) (p Provider, err error) {
	var domains, groups string
	err = row.Scan(&p.ID, &p.Name, &p.Kind, &p.Purpose, &p.Enabled, &p.AllowMembers, &p.ClientID, &p.SecretEnc, &p.MicrosoftTenantID, &p.IssuerURL, &p.PKCE, &p.AutoProvision, &domains, &groups, &p.GroupMatch, &p.GroupsClaim, &p.LegacyKey, &p.Revision)
	if err != nil {
		return p, err
	}
	p.SecretSet = p.SecretEnc != ""
	if err = json.Unmarshal([]byte(domains), &p.Domains); err != nil {
		return p, err
	}
	err = json.Unmarshal([]byte(groups), &p.GroupIDs)
	if p.Domains == nil {
		p.Domains = []string{}
	}
	if p.GroupIDs == nil {
		p.GroupIDs = []string{}
	}
	return
}

type Query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func Get(ctx context.Context, q Query, id string) (Provider, error) {
	return Scan(q.QueryRowContext(ctx, `SELECT `+Columns+` FROM identity_providers WHERE id=?`, id))
}
func Insert(ctx context.Context, tx *sql.Tx, p Provider) error {
	d, _ := json.Marshal(p.Domains)
	g, _ := json.Marshal(p.GroupIDs)
	_, err := tx.ExecContext(ctx, `INSERT INTO identity_providers(`+Columns+`)VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, p.ID, p.Name, p.Kind, p.Purpose, p.Enabled, p.AllowMembers, p.ClientID, p.SecretEnc, p.MicrosoftTenantID, p.IssuerURL, p.PKCE, p.AutoProvision, string(d), string(g), p.GroupMatch, p.GroupsClaim, p.LegacyKey, p.Revision)
	return err
}

// CalendarConfig resolves only the app bound to this exact encrypted token row.
// A concurrent reconnect cannot pair an old token with another app's credentials.
func CalendarConfig(ctx context.Context, db *sql.DB, key [32]byte, kind, user, email, access string, fallback *oauth2.Config) (*oauth2.Config, string, error) {
	var id string
	if err := db.QueryRowContext(ctx, `SELECT oauth_provider_id FROM calendar_connections WHERE user_id=? AND provider=? AND account_email=? AND access_token_enc=?`, user, kind, email, access).Scan(&id); err != nil {
		return nil, "", fmt.Errorf("calendar connection changed; reconnect or retry: %w", err)
	}
	if id == "" {
		return fallback, "", nil
	}
	p, err := Get(ctx, db, id)
	if err != nil || p.Kind != kind || !p.Integrations() {
		return nil, "", fmt.Errorf("calendar application is disabled or unavailable; contact your administrator")
	}
	clientSecret, err := secret.Decrypt(key, p.SecretEnc)
	if err != nil {
		return nil, "", err
	}
	cfg := *fallback
	cfg.ClientID = p.ClientID
	cfg.ClientSecret = clientSecret
	switch kind {
	case "google":
		cfg.Endpoint = google.Endpoint
	case "microsoft":
		tenant := strings.TrimSpace(p.MicrosoftTenantID)
		if tenant == "" {
			tenant = "common"
		}
		cfg.Endpoint = microsoft.AzureADEndpoint(tenant)
	default:
		return nil, "", fmt.Errorf("unsupported calendar application")
	}
	return &cfg, id, nil
}

// CheckRefreshBinding runs in the token-save transaction. A stale refresh may
// neither resurrect a disconnected account nor replace a different app's grant.
func CheckRefreshBinding(ctx context.Context, tx *sql.Tx, user, kind, email, id string) error {
	var current string
	if err := tx.QueryRowContext(ctx, `SELECT oauth_provider_id FROM calendar_connections WHERE user_id=? AND provider=? AND account_email=?`, user, kind, email).Scan(&current); err != nil {
		return err
	}
	if current != id {
		return fmt.Errorf("calendar application changed during token refresh")
	}
	if id != "" {
		p, err := Get(ctx, tx, id)
		if err != nil || p.Kind != kind || !p.Integrations() {
			return fmt.Errorf("calendar application was disabled during token refresh")
		}
	}
	return nil
}
