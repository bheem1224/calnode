package db

import (
	"context"
	"github.com/calnode/calnode/internal/access"
	"github.com/pressly/goose/v3"
	"testing"
)

func TestPermissionMigrationFreezesLegacyGrantsAndRecovery(t *testing.T) {
	database, err := Open("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	goose.SetBaseFS(migrations)
	if err = goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err = goose.UpTo(database, "migrations", 92); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO users(id,email,name,is_admin) VALUES('admin','admin@example.com','Admin',1)`,
		`INSERT INTO users(id,email,name) VALUES('member','member@example.com','Member')`,
		`INSERT INTO api_keys(id,user_id,name,key_hash) VALUES('admin-key','admin','Admin legacy','hash-a')`,
		`INSERT INTO api_keys(id,user_id,name,key_hash) VALUES('member-key','member','Member legacy','hash-m')`,
	} {
		if _, err = database.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err = Migrate(database); err != nil {
		t.Fatal(err)
	}
	var enabled int
	if err = database.QueryRow(`SELECT ownership_recovery_enabled FROM server_settings`).Scan(&enabled); err != nil || enabled != 1 {
		t.Fatalf("recovery=%d err=%v", enabled, err)
	}
	var raw string
	var legacy bool
	if err = database.QueryRow(`SELECT permissions_json,is_legacy FROM api_keys WHERE id='member-key'`).Scan(&raw, &legacy); err != nil {
		t.Fatal(err)
	}
	frozen := access.Parse(raw)
	if !legacy || frozen["bookings.read"] != "own" || frozen.Has("members.invite_admin") {
		t.Fatalf("unexpected migrated member grants: %s", raw)
	}
	if _, err = database.Exec(`UPDATE users SET is_admin=1 WHERE id='member'`); err != nil {
		t.Fatal(err)
	}
	current, err := access.Load(context.Background(), database, "member")
	if err != nil {
		t.Fatal(err)
	}
	if access.Intersect(current, frozen).Has("members.invite_admin") {
		t.Fatal("promotion widened legacy key")
	}
	if _, err = database.Exec(`UPDATE users SET is_owner=1 WHERE id='admin'`); err != nil {
		t.Fatal(err)
	}
	if _, err = database.Exec(`UPDATE users SET is_owner=1 WHERE id='member'`); err == nil {
		t.Fatal("multiple owners accepted")
	}
	if err = goose.DownTo(database, "migrations", 92); err != nil {
		t.Fatalf("rollback: %v", err)
	}
}
