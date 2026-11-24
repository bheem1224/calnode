package gcal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestExchangeRejectsMissingAccountIdentityWithoutReplacingConnection(t *testing.T) {
	for _, identity := range []string{"denied", "empty"} {
		t.Run(identity, func(t *testing.T) {
			c := newTestClient(t)
			seedUser(t, c.db, "u")
			ctx := context.Background()
			if err := c.saveToken(ctx, "u", "primary", "", &oauth2.Token{AccessToken: "old", Expiry: time.Now().Add(time.Hour)}); err != nil {
				t.Fatal(err)
			}
			var before string
			if err := c.db.QueryRow(`SELECT access_token_enc FROM calendar_connections WHERE user_id='u'`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/token" {
					_, _ = w.Write([]byte(`{"access_token":"new","token_type":"Bearer","expires_in":3600}`))
					return
				}
				if identity == "denied" {
					w.WriteHeader(403)
				}
				_, _ = w.Write([]byte(`{}`))
			}))
			defer srv.Close()
			c.config.Endpoint.TokenURL = srv.URL + "/token"
			c.config.Endpoint.AuthStyle = oauth2.AuthStyleInParams
			c.apiBase = srv.URL
			if err := c.Exchange(ctx, "u", "code", "primary"); err == nil {
				t.Fatal("accepted unidentified calendar connection")
			}
			var after string
			if err := c.db.QueryRow(`SELECT access_token_enc FROM calendar_connections WHERE user_id='u'`).Scan(&after); err != nil || before != after {
				t.Fatal("existing connection replaced after failed identity read", err)
			}
		})
	}
}
