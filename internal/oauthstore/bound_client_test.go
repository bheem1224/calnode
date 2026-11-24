package oauthstore

import (
	"context"
	"fmt"
	"github.com/calnode/calnode/internal/db"
	"github.com/calnode/calnode/internal/providerhttp"
	"github.com/calnode/calnode/internal/secret"
	"golang.org/x/oauth2"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBoundClientsRefreshOnceAndPersistRotation(t *testing.T) {
	for _, provider := range []string{"google", "microsoft"} {
		t.Run(provider, func(t *testing.T) {
			database, e := db.Open("sqlite://:memory:")
			if e != nil {
				t.Fatal(e)
			}
			defer database.Close()
			if e = db.Migrate(database); e != nil {
				t.Fatal(e)
			}
			var key [32]byte
			access, _ := secret.Encrypt(key, "expired")
			refresh, _ := secret.Encrypt(key, "original-refresh")
			_, e = database.Exec(`INSERT INTO users(id,email,name)VALUES('host','host@example.test','Host');`)
			if e != nil {
				t.Fatal(e)
			}
			_, e = database.Exec(`INSERT INTO calendar_connections(id,user_id,provider,account_email,access_token_enc,refresh_token_enc,calendar_id,expiry_at)VALUES('conn','host',?,'account@example.test',?,?,'primary','2000-01-01T00:00:00Z')`, provider, access, refresh)
			if e != nil {
				t.Fatal(e)
			}
			var calls atomic.Int32
			var reject atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				if reject.Load() {
					w.WriteHeader(400)
					fmt.Fprint(w, `{"error":"invalid_grant"}`)
					return
				}
				_ = r.ParseForm()
				if r.Form.Get("refresh_token") != "original-refresh" {
					t.Error("stale or unexpected refresh token")
				}
				time.Sleep(15 * time.Millisecond)
				fmt.Fprint(w, `{"access_token":"fresh-access","refresh_token":"rotated-refresh","expires_in":3600,"token_type":"Bearer"}`)
			}))
			defer server.Close()
			cfg := &oauth2.Config{ClientID: "app", ClientSecret: "secret", Endpoint: oauth2.Endpoint{TokenURL: server.URL, AuthStyle: oauth2.AuthStyleInParams}}
			m := providerhttp.New(database)
			var wg sync.WaitGroup
			for i := 0; i < 20; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					hc := BoundClient(context.Background(), database, key, provider, "host", "account@example.test", "", cfg, m)
					token, e := hc.Transport.(*oauth2.Transport).Source.Token()
					if e != nil || token.AccessToken != "fresh-access" {
						t.Errorf("token: %v", e)
					}
				}()
			}
			wg.Wait()
			if calls.Load() != 1 {
				t.Fatalf("%d refreshes", calls.Load())
			}
			var raw string
			_ = database.QueryRow(`SELECT refresh_token_enc FROM calendar_connections WHERE id='conn'`).Scan(&raw)
			value, _ := secret.Decrypt(key, raw)
			if value != "rotated-refresh" {
				t.Fatal("rotation not persisted")
			}
			// A revoked grant is attempted once, then held until a reconnect changes it.
			reject.Store(true)
			_, _ = database.Exec(`UPDATE calendar_connections SET expiry_at='2000-01-01T00:00:00Z'`)
			before := calls.Load()
			for i := 0; i < 5; i++ {
				hc := BoundClient(context.Background(), database, key, provider, "host", "account@example.test", "", cfg, m)
				if _, e = hc.Transport.(*oauth2.Transport).Source.Token(); e == nil {
					t.Fatal("revoked grant accepted")
				}
			}
			if calls.Load() != before+1 {
				t.Fatal("revoked refresh was hammered")
			}
			// A successful explicit reconnect clears the held grant error.
			reconnected, _ := secret.Encrypt(key, "reconnected-access")
			if _, e = database.Exec(`UPDATE calendar_connections SET access_token_enc=?,expiry_at=?`, reconnected, time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)); e != nil {
				t.Fatal(e)
			}
			restored := BoundClient(context.Background(), database, key, provider, "host", "account@example.test", "", cfg, m)
			if tok, err := restored.Transport.(*oauth2.Transport).Source.Token(); err != nil || tok.AccessToken != "reconnected-access" {
				t.Fatal("reconnected grant remained blocked", err)
			}
			// Disconnect is checked against live storage even if clients already exist.
			_, _ = database.Exec(`DELETE FROM calendar_connections`)
			if _, e = restored.Transport.(*oauth2.Transport).Source.Token(); e == nil {
				t.Fatal("disconnected grant retained")
			}
		})
	}
}
