package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	wa "github.com/go-webauthn/webauthn/webauthn"
)

type passkeyUser struct {
	ID, Email, Name string
	Credentials     []wa.Credential
	Stored          map[string]string
}

func (u passkeyUser) WebAuthnID() []byte                   { return []byte(u.ID) }
func (u passkeyUser) WebAuthnName() string                 { return u.Email }
func (u passkeyUser) WebAuthnDisplayName() string          { return u.Name }
func (u passkeyUser) WebAuthnCredentials() []wa.Credential { return u.Credentials }
func (h *Handler) webAuthn() (*wa.WebAuthn, error) {
	u, err := url.Parse(h.baseURL)
	if err == nil && net.ParseIP(u.Hostname()) != nil {
		return nil, fmt.Errorf("passkeys require a DNS hostname; use http://localhost for local development")
	}
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && !(u.Scheme == "http" && u.Hostname() == "localhost")) {
		return nil, fmt.Errorf("passkeys require an HTTPS identity URL (localhost is allowed for development)")
	}
	return wa.New(&wa.Config{RPID: u.Hostname(), RPDisplayName: "Calnode", RPOrigins: []string{u.Scheme + "://" + u.Host}, AuthenticatorSelection: protocol.AuthenticatorSelection{ResidentKey: protocol.ResidentKeyRequirementRequired, UserVerification: protocol.VerificationRequired}})
}
func (h *Handler) passkeyAccount(ctx context.Context, id string) (passkeyUser, error) {
	u := passkeyUser{Stored: map[string]string{}}
	if err := h.db.QueryRowContext(ctx, `SELECT id,email,name FROM users WHERE id=? AND archived_at IS NULL`, id).Scan(&u.ID, &u.Email, &u.Name); err != nil {
		return u, err
	}
	rows, err := h.db.QueryContext(ctx, `SELECT credential_json FROM passkeys WHERE user_id=?`, id)
	if err != nil {
		return u, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var c wa.Credential
		if err = rows.Scan(&raw); err != nil {
			return u, err
		}
		if err = json.Unmarshal([]byte(raw), &c); err != nil {
			return u, err
		}
		u.Credentials = append(u.Credentials, c)
		u.Stored[base64.RawURLEncoding.EncodeToString(c.ID)] = raw
	}
	return u, rows.Err()
}
func (h *Handler) BeginPasskeyRegistration(w http.ResponseWriter, r *http.Request) {
	actor, _ := userFromContext(r.Context())
	if !h.nativeEnrollmentAllowed(r.Context(), actor) {
		h.writeError(w, 403, "native authentication is disabled")
		return
	}
	if !h.freshAuthFactor(w, r, actor) {
		return
	}
	auth, err := h.webAuthn()
	if err != nil {
		h.writeError(w, 400, err.Error())
		return
	}
	u, err := h.passkeyAccount(r.Context(), actor.ID)
	if err != nil {
		h.writeError(w, 500, "could not load passkeys")
		return
	}
	options, session, err := auth.BeginRegistration(u)
	if err != nil {
		h.writeError(w, 400, "could not start passkey registration")
		return
	}
	token, err := h.authChallenge(r.Context(), actor.ID, "register:"+actor.SessionID, session)
	if err != nil {
		h.writeError(w, 500, "could not store challenge")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	h.writeJSON(w, 200, map[string]any{"options": options, "token": token})
}
func (h *Handler) FinishPasskeyRegistration(w http.ResponseWriter, r *http.Request) {
	actor, _ := userFromContext(r.Context())
	if !h.nativeEnrollmentAllowed(r.Context(), actor) {
		h.writeError(w, 403, "native authentication is disabled")
		return
	}
	if !h.freshAuthFactor(w, r, actor) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 65536)
	token := r.Header.Get("X-Auth-Challenge")
	id, payload, err := h.consumeAuthChallenge(r.Context(), token, "register:"+actor.SessionID)
	if err != nil || id != actor.ID {
		h.writeError(w, 400, "passkey challenge expired")
		return
	}
	var session wa.SessionData
	if json.Unmarshal([]byte(payload), &session) != nil {
		h.writeError(w, 400, "invalid challenge")
		return
	}
	auth, err := h.webAuthn()
	if err != nil {
		h.writeError(w, 400, err.Error())
		return
	}
	u, err := h.passkeyAccount(r.Context(), actor.ID)
	if err != nil {
		h.writeError(w, 400, "account unavailable")
		return
	}
	cred, err := auth.FinishRegistration(u, session, r)
	if err != nil {
		h.writeError(w, 400, "passkey could not be verified; try again")
		return
	}
	name := strings.TrimSpace(r.Header.Get("X-Passkey-Name"))
	if name == "" {
		name = "Passkey"
	}
	if len(name) > 80 {
		name = name[:80]
	}
	raw, err := json.Marshal(cred)
	if err != nil {
		h.writeError(w, 500, "could not encode passkey")
		return
	}
	_, err = h.db.ExecContext(r.Context(), `INSERT INTO passkeys(id,user_id,name,credential_json)VALUES(?,?,?,?)`, base64.RawURLEncoding.EncodeToString(cred.ID), actor.ID, name, string(raw))
	if err != nil {
		h.writeError(w, 409, "passkey is already registered")
		return
	}
	if !h.markMFAConfirmed(w, r, actor) {
		return
	}
	h.securityNotice(r.Context(), actor.ID, "Passkey enrolled", name)
	h.securityAudit(r.Context(), actor.ID, "auth.passkey.enroll", actor.ID, nil)
	h.writeJSON(w, 200, map[string]bool{"ok": true})
}
func (h *Handler) BeginPasskeyLogin(w http.ResponseWriter, r *http.Request) {
	auth, err := h.webAuthn()
	if err != nil {
		h.writeError(w, 400, err.Error())
		return
	}
	options, session, err := auth.BeginDiscoverableLogin()
	if err != nil {
		h.writeError(w, 400, "could not start passkey sign-in")
		return
	}
	id, purpose := "", "passkey-login"
	if r.URL.Query().Get("step_up") == "1" {
		if current, _, ok := h.sessionUser(r); ok {
			c, e := r.Cookie(sessionCookieName)
			if e == nil {
				id = current
				purpose = "passkey-stepup:" + c.Value
			}
		}
		if id == "" {
			h.writeError(w, 401, "sign in again")
			return
		}
	}
	token, err := h.authChallenge(r.Context(), id, purpose, session)
	if err != nil {
		h.writeError(w, 500, "could not store challenge")
		return
	}
	h.setPendingAuth(w, token)
	h.writeJSON(w, 200, map[string]any{"options": options, "step_up": id != ""})
}
func (h *Handler) FinishPasskeyLogin(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 65536)
	c, err := r.Cookie(pendingAuthCookie)
	if err != nil {
		h.writeError(w, 401, "passkey challenge expired")
		return
	}
	purpose := "passkey-login"
	stepID := ""
	sessionID := ""
	if r.URL.Query().Get("step_up") == "1" {
		var ok bool
		stepID, _, ok = h.sessionUser(r)
		cookie, e := r.Cookie(sessionCookieName)
		if !ok || e != nil {
			h.writeError(w, 401, "sign in again")
			return
		}
		sessionID = cookie.Value
		purpose = "passkey-stepup:" + sessionID
	}
	bound, payload, err := h.consumeAuthChallenge(r.Context(), c.Value, purpose)
	if err != nil || bound != stepID {
		h.writeError(w, 401, "passkey challenge expired")
		return
	}
	var session wa.SessionData
	if json.Unmarshal([]byte(payload), &session) != nil {
		h.writeError(w, 400, "invalid challenge")
		return
	}
	auth, err := h.webAuthn()
	if err != nil {
		h.writeError(w, 400, err.Error())
		return
	}
	var account passkeyUser
	cred, err := auth.FinishDiscoverableLogin(func(rawID, handle []byte) (wa.User, error) {
		u, e := h.passkeyAccount(r.Context(), string(handle))
		if e != nil {
			return nil, e
		}
		if stepID != "" && u.ID != stepID {
			return nil, fmt.Errorf("wrong account")
		}
		account = u
		return u, nil
	}, session, r)
	if err != nil || cred == nil || cred.Authenticator.CloneWarning {
		h.writeError(w, 401, "passkey verification failed")
		return
	}
	if stepID == "" && !h.passwordAllowed(r.Context(), account.ID) {
		h.writeError(w, 403, "use your configured SSO sign-in")
		return
	}
	raw, err := json.Marshal(cred)
	if err != nil {
		h.writeError(w, 500, "could not update passkey")
		return
	}
	res, err := h.db.ExecContext(r.Context(), `UPDATE passkeys SET credential_json=? WHERE id=? AND user_id=? AND credential_json=?`, string(raw), base64.RawURLEncoding.EncodeToString(cred.ID), account.ID, account.Stored[base64.RawURLEncoding.EncodeToString(cred.ID)])
	if err != nil {
		h.writeError(w, 401, "passkey is no longer available")
		return
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		h.writeError(w, 401, "passkey is no longer available")
		return
	}
	if stepID != "" {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		_, err = h.db.ExecContext(r.Context(), `UPDATE sessions SET mfa_verified_at=?,reauthenticated_at=? WHERE id=?`, now, now, sessionID)
	} else {
		err = h.createSessionMethod(context.WithValue(r.Context(), ctxMFAVerified, true), w, account.ID, "passkey")
	}
	if err != nil {
		h.writeError(w, 401, "sign in again")
		return
	}
	h.clearPendingAuth(w)
	h.writeJSON(w, 200, map[string]bool{"ok": true})
}

// Reauthenticate accepts OTP/recovery proof alongside passwords. A password cannot
// establish fresh MFA when the account already has another enrolled factor.
func (h *Handler) verifyReauthCode(w http.ResponseWriter, r *http.Request, u AuthUser, code string) bool {
	if code == "" || !h.verifyNativeCode(r.Context(), u.ID, code) {
		return false
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := h.db.ExecContext(r.Context(), `UPDATE sessions SET mfa_verified_at=?,reauthenticated_at=? WHERE id=?`, now, now, u.SessionID); err != nil {
		h.writeError(w, 500, "could not verify session")
		return true
	}
	h.writeJSON(w, 200, map[string]bool{"ok": true})
	return true
}
