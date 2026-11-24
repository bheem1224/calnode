package providerhttp

import (
	"context"
	"golang.org/x/oauth2"
	"net/http"
	"time"
)

type managerKey struct{}

// WithManager lets authentication helpers share the instance's API budgets.
func WithManager(ctx context.Context, m *Manager) context.Context {
	return context.WithValue(ctx, managerKey{}, m)
}
func FromContext(ctx context.Context) *Manager {
	if m, ok := ctx.Value(managerKey{}).(*Manager); ok && m != nil {
		return m
	}
	return Default
}

// Coordinate also supports short-lived OAuth clients (identity/group reads at
// sign-in). Clone transports rather than mutating a shared configured client.
func (m *Manager) Coordinate(client *http.Client, app, account string) *http.Client {
	clone := *client
	if auth, ok := client.Transport.(*oauth2.Transport); ok {
		copy := *auth
		copy.Base = m.Attach(auth.Base, app, account)
		clone.Transport = &copy
	} else {
		clone.Transport = m.Attach(client.Transport, app, account)
	}
	if clone.Timeout == 0 || clone.Timeout > 20*time.Second {
		clone.Timeout = 20 * time.Second
	}
	clone.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &clone
}
