package handler

import (
	"context"
	"github.com/calnode/calnode/internal/db"
	"log/slog"
	"testing"
)

func TestDynamicGroupHostsWithoutPublicHandles(t *testing.T) {
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err = db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`INSERT INTO users(id,email,name) VALUES('person-a','a@example.com','Person A')`,
		`INSERT INTO users(id,email,name,handle) VALUES('person-b','b@example.com','Person B','public-b')`,
		`INSERT INTO users(id,email,name,archived_at) VALUES('archived','c@example.com','Archived','2026-01-01')`,
	} {
		if _, err = database.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	h := New(database, slog.Default())
	hosts, err := h.resolveGroupHosts(context.Background(), "id:person-a+public-b")
	if err != nil || len(hosts) != 2 {
		t.Fatalf("hosts=%v err=%v", hosts, err)
	}
	if hosts[0].ID != "person-a" || hosts[1].ID != "person-b" {
		t.Fatalf("wrong hosts: %v", hosts)
	}
	if _, err = h.resolveGroupHosts(context.Background(), "id:person-a+id:archived"); err == nil {
		t.Fatal("archived host accepted")
	}
	if _, err = database.Exec(`UPDATE users SET handle='new-handle' WHERE id='person-a'`); err != nil {
		t.Fatal(err)
	}
	if _, err = h.resolveGroupHosts(context.Background(), "id:person-a+id:person-b"); err != nil {
		t.Fatalf("stable reference failed after handle change: %v", err)
	}
	if _, err = database.Exec(`UPDATE users SET handle=NULL WHERE id='person-a'`); err != nil {
		t.Fatal(err)
	}
	var handle string
	if err = database.QueryRow(`SELECT COALESCE(handle,'') FROM users WHERE id='person-a'`).Scan(&handle); err != nil || handle != "" {
		t.Fatal("public handle was changed")
	}
}
