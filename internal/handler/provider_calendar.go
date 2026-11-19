package handler

import (
	"fmt"
	"net/http"

	"github.com/calnode/calnode/internal/access"
	"github.com/calnode/calnode/internal/calendar"
	"github.com/calnode/calnode/internal/calendar/microsoft"
	"github.com/calnode/calnode/internal/gcal"
	ip "github.com/calnode/calnode/internal/identityprovider"
	"golang.org/x/oauth2"
)

func withCalendarPKCE(pr calendar.Provider, verifier string) calendar.Provider {
	switch c := pr.(type) {
	case *gcal.Client:
		return c.WithPKCE(verifier)
	case *microsoft.Client:
		return c.WithPKCE(verifier)
	}
	return pr
}
func (h *Handler) connectProviderCalendar(w http.ResponseWriter, r *http.Request) {
	if h.demoMode {
		h.writeError(w, 403, "not available in demo mode")
		return
	}
	u, _ := userFromContext(r.Context())
	if u.SessionID == "" || u.ImpersonatorID != "" {
		h.writeError(w, 403, "connect calendars from your own signed-in browser")
		return
	}
	id := r.URL.Query().Get("provider_id")
	if id == "" {
		ps, err := h.listIdentityProviders(r.Context())
		if err != nil {
			h.writeError(w, 503, "provider settings unavailable")
			return
		}
		for _, p := range ps {
			if p.Integrations() && (r.URL.Query().Get("provider") == "" || r.URL.Query().Get("provider") == p.Kind) {
				id = p.ID
				break
			}
		}
	}
	p, err := ip.Get(r.Context(), h.db, id)
	if err != nil || !p.Integrations() {
		h.writeError(w, 400, "choose an enabled calendar application")
		return
	}
	pr, err := h.calendarClient(p)
	if err != nil {
		h.writeError(w, 503, "calendar application could not be loaded")
		return
	}
	intent := providerIntent{ProviderID: p.ID, Revision: p.Revision, SessionID: u.SessionID, LinkUser: u.ID}
	if p.PKCE {
		intent.Verifier = oauth2.GenerateVerifier()
	}
	state, err := h.authChallenge(r.Context(), u.ID, "provider-calendar", intent)
	if err != nil {
		h.writeError(w, 500, "could not start calendar connection")
		return
	}
	h.setProviderState(w, state)
	http.Redirect(w, r, withCalendarPKCE(pr, intent.Verifier).AuthURL(state), 302)
}
func (h *Handler) callbackProviderCalendar(w http.ResponseWriter, r *http.Request) {
	intent, err := h.consumeProviderIntent(w, r, "provider-calendar")
	if err != nil {
		h.writeError(w, 400, "calendar connection expired or state is invalid; connect again")
		return
	}
	user, _, ok := h.sessionUser(r)
	cookie, e := r.Cookie(sessionCookieName)
	if !ok || e != nil || user != intent.LinkUser || cookie.Value != intent.SessionID {
		h.writeError(w, 401, "sign in again before connecting this calendar")
		return
	}
	grants, e := access.Load(r.Context(), h.db, user)
	if e != nil || !grants.Has("integrations.update") {
		h.writeError(w, 403, "calendar connection is no longer authorized")
		return
	}
	p, e := ip.Get(r.Context(), h.db, intent.ProviderID)
	if e != nil || !p.Integrations() || p.Revision != intent.Revision {
		h.writeError(w, 403, "calendar application changed; connect again")
		return
	}
	if r.URL.Query().Get("error") != "" || r.URL.Query().Get("code") == "" {
		h.writeError(w, 400, "calendar authorization was not completed")
		return
	}
	pr, e := h.calendarClient(p)
	if e != nil {
		h.writeError(w, 503, "calendar application unavailable")
		return
	}
	if e = withCalendarPKCE(pr, intent.Verifier).Exchange(providerHTTPContext(r.Context()), user, r.URL.Query().Get("code"), "primary"); e != nil {
		h.logger.Warn("provider calendar exchange failed", "provider_id", p.ID, "error", e)
		h.writeError(w, 400, "could not connect the calendar; check the account and application permissions")
		return
	}
	h.securityAudit(r.Context(), user, "calendar.connect", p.ID, nil)
	http.Redirect(w, r, fmt.Sprintf("%s/admin/calendar?connected=true", h.baseURL), 302)
}
