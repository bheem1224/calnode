package main

import (
	"context"
	"fmt"
	"github.com/calnode/calnode/internal/authsecurity"
	"github.com/calnode/calnode/internal/config"
	"github.com/calnode/calnode/internal/db"
	"os"
	"strings"
)

// Emergency recovery is deliberately a local server operation, never a public HTTP endpoint.
func runRecoverOwner(args []string) {
	if len(args) != 1 || args[0] != "--confirm" {
		fmt.Fprintln(os.Stderr, "Emergency Owner recovery revokes Owner authentication methods, sessions, API keys and agent connections. Run on the server with: calnode recover-owner --confirm")
		os.Exit(1)
	}
	cfg := config.Load()
	database, err := db.Open(cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "could not open the configured database")
		os.Exit(1)
	}
	defer database.Close()
	if err = db.Migrate(database); err != nil {
		fmt.Fprintln(os.Stderr, "could not migrate database")
		os.Exit(1)
	}
	var id, email string
	if err = database.QueryRow(`SELECT id,email FROM users WHERE is_owner=1 AND archived_at IS NULL`).Scan(&id, &email); err != nil {
		fmt.Fprintln(os.Stderr, "no active Owner found; this command does not change ownership")
		os.Exit(1)
	}
	token, err := authsecurity.Recover(context.Background(), database, id, "server-operator", "Emergency server Owner recovery; temporary owner-bound access only", true, false)
	if err != nil {
		fmt.Fprintln(os.Stderr, "could not authorize Owner recovery")
		os.Exit(1)
	}
	fmt.Printf("Owner recovery for %s expires in 15 minutes. Enroll replacement authentication to receive one hour of emergency Owner access, then repair normal sign-in settings. Other accounts' sign-in settings are unchanged. Treat this link as a credential:\n%s/admin/auth/recover#token=%s\n", email, strings.TrimRight(cfg.BaseURL, "/"), token)
}
