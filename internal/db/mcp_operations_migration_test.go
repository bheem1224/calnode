package db

import (
	"testing"

	"github.com/calnode/calnode/internal/access"
	"github.com/pressly/goose/v3"
)

func TestMCPMigrationPreservesCredentialCeilingsAndRollback(t *testing.T) {
	database, err := Open("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	goose.SetBaseFS(migrations)
	if err = goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err = goose.UpTo(database, "migrations", 99); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`INSERT INTO users(id,email,name)VALUES('user','user@example.test','User')`,
		`INSERT INTO api_keys(id,user_id,name,key_hash,permissions_json)VALUES('link-key','user','Link','hash-link','{"event_types.links":"own"}')`,
		`INSERT INTO api_keys(id,user_id,name,key_hash,permissions_json)VALUES('read-key','user','Read','hash-read','{"event_types.read":"team"}')`,
	} {
		if _, err = database.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err = Migrate(database); err != nil {
		t.Fatal(err)
	}
	var required bool
	if err = database.QueryRow(`SELECT mcp_review_required FROM users WHERE id='user'`).Scan(&required); err != nil || !required {
		t.Fatal("existing user did not default to review", err)
	}
	for _, id := range []string{"link-key", "read-key"} {
		var raw string
		if err = database.QueryRow(`SELECT permissions_json FROM api_keys WHERE id=?`, id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		grants := access.Parse(raw)
		if grants.Has("team_event_types.links") || grants.Has("team_event_types.update") {
			t.Fatal("migration expanded team authority")
		}
		if id == "link-key" && grants["event_types.custom_times"] != "own" {
			t.Fatal("existing custom-time capability changed scope")
		}
		if id == "read-key" && grants.Has("event_types.custom_times") {
			t.Fatal("read-only key gained custom times")
		}
	}
	if err = goose.DownTo(database, "migrations", 99); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err = database.QueryRow(`SELECT permissions_json FROM api_keys WHERE id='link-key'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	grants := access.Parse(raw)
	if grants.Has("event_types.custom_times") || grants["event_types.links"] != "own" {
		t.Fatal("rollback damaged the original ceiling")
	}
}
