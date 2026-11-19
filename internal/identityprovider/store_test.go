package identityprovider

import (
	"context"
	"testing"

	"github.com/calnode/calnode/internal/db"
	"github.com/calnode/calnode/internal/secret"
	"golang.org/x/oauth2"
)

func TestCalendarCredentialsAndRefreshRemainBoundToIssuingApp(t *testing.T) {
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err = db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var key [32]byte
	enc, err := secret.Encrypt(key, "personal-secret")
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(`INSERT INTO users(id,email,name)VALUES('host','host@example.com','Host')`)
	if err != nil {
		t.Fatal(err)
	}
	tx, _ := database.Begin()
	for _, p := range []Provider{{ID: "company-sso", Name: "Company SSO", Kind: "microsoft", Purpose: "sso", Enabled: true, ClientID: "tenant-client", SecretEnc: enc, MicrosoftTenantID: "11111111-1111-4111-8111-111111111111", Domains: []string{}, GroupIDs: []string{}, GroupMatch: "any", GroupsClaim: "groups", Revision: 1}, {ID: "personal", Name: "Personal", Kind: "microsoft", Purpose: "integrations", Enabled: true, ClientID: "personal-client", SecretEnc: enc, MicrosoftTenantID: "common", Domains: []string{}, GroupIDs: []string{}, GroupMatch: "any", GroupsClaim: "groups", Revision: 1}} {
		if err = Insert(ctx, tx, p); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(`INSERT INTO calendar_connections(id,user_id,provider,account_email,calendar_id,access_token_enc,oauth_provider_id)VALUES('grant','host','microsoft','personal@example.com','primary','old-encrypted-token','personal')`)
	if err != nil {
		t.Fatal(err)
	}
	cfg, id, err := CalendarConfig(ctx, database, key, "microsoft", "host", "personal@example.com", "old-encrypted-token", &oauth2.Config{ClientID: "wrong-app", ClientSecret: "wrong-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if id != "personal" || cfg.ClientID != "personal-client" || cfg.ClientSecret != "personal-secret" || cfg.Endpoint.TokenURL != "https://login.microsoftonline.com/common/oauth2/v2.0/token" {
		t.Fatalf("wrong app binding %+v %s", cfg, id)
	}
	if _, _, err = CalendarConfig(ctx, database, key, "microsoft", "host", "personal@example.com", "another-token", cfg); err == nil {
		t.Fatal("stale token paired with current app")
	}
	check := func(id string) error {
		tx, _ := database.Begin()
		defer tx.Rollback()
		return CheckRefreshBinding(ctx, tx, "host", "microsoft", "personal@example.com", id)
	}
	if err = check("personal"); err != nil {
		t.Fatal(err)
	}
	if check("company-sso") == nil {
		t.Fatal("reconnected grant replaced by stale refresh")
	}
	_, _ = database.Exec(`UPDATE identity_providers SET enabled=0 WHERE id='personal'`)
	if _, _, err = CalendarConfig(ctx, database, key, "microsoft", "host", "personal@example.com", "old-encrypted-token", cfg); err == nil {
		t.Fatal("disabled app still used")
	}
	if check("personal") == nil {
		t.Fatal("disabled app refresh persisted")
	}
	_, _ = database.Exec(`DELETE FROM calendar_connections`)
	if check("personal") == nil {
		t.Fatal("refresh resurrected disconnect")
	}
}
