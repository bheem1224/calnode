package handler

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"net/http"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/abuse"
	"github.com/calnode/calnode/internal/access"
	"github.com/calnode/calnode/internal/authsecurity"
	"github.com/calnode/calnode/internal/mailer"
	"github.com/calnode/calnode/internal/secret"
	"github.com/calnode/calnode/internal/uid"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

const ctxMFAVerified contextKey = "mfa_verified"
const ctxAuthSetup contextKey = "auth_setup"
const pendingAuthCookie = "calnode_auth_pending"

var errNativeRecoveryRequired = errors.New("use the recovery link authorized by your administrator")

func randomAuthToken() (string, error) { return authsecurity.Token() }
func (h *Handler) hasMFA(ctx context.Context, id string) bool {
	var n int
	err := h.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM user_totp WHERE user_id=? AND confirmed=1)+(SELECT COUNT(*) FROM passkeys WHERE user_id=?)`, id, id).Scan(&n)
	return err != nil || n > 0
}
func (h *Handler) mfaRequired(ctx context.Context, id string) bool {
	p, err := h.signinPolicy(ctx)
	if err != nil {
		return true
	}
	var admin, force bool
	if h.db.QueryRowContext(ctx, `SELECT (is_admin=1 OR is_owner=1),mfa_reenroll_required FROM users WHERE id=? AND archived_at IS NULL`, id).Scan(&admin, &force) != nil {
		return true
	}
	return force || (admin && p.RequireAdminMFA) || (!admin && p.RequireMemberMFA)
}
func (h *Handler) authChallenge(ctx context.Context, id, purpose string, payload any) (string, error) {
	// Bound storage for anonymous passkey challenges even on a quiet instance.
	if _, err := h.db.ExecContext(ctx, `DELETE FROM auth_challenges WHERE expires_at<=?`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return "", err
	}
	raw, err := randomAuthToken()
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	_, err = h.db.ExecContext(ctx, `INSERT INTO auth_challenges(token_hash,user_id,purpose,payload,expires_at)VALUES(?,NULLIF(?,''),?,?,?)`, authsecurity.Hash(raw), id, purpose, string(b), time.Now().UTC().Add(5*time.Minute).Format(time.RFC3339Nano))
	return raw, err
}
func (h *Handler) setPendingAuth(w http.ResponseWriter, raw string) {
	http.SetCookie(w, &http.Cookie{Name: pendingAuthCookie, Value: raw, Path: "/", Secure: h.secureCookie, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 300})
}
func (h *Handler) clearPendingAuth(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: pendingAuthCookie, Path: "/", Secure: h.secureCookie, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
}

// No authenticated application session is issued until the second factor succeeds.
func (h *Handler) nativeSessionGate(ctx context.Context, w http.ResponseWriter, id, method string) (bool, error) {
	verified, _ := ctx.Value(ctxMFAVerified).(bool)
	setup, _ := ctx.Value(ctxAuthSetup).(bool)
	if verified || setup || isSSOMethod(method) || method == "passkey" {
		return false, nil
	}
	if !h.passwordAllowed(ctx, id) {
		return true, fmt.Errorf("native sign-in is disabled for this account")
	}
	var reenroll bool
	if h.db.QueryRowContext(ctx, `SELECT mfa_reenroll_required FROM users WHERE id=?`, id).Scan(&reenroll) != nil {
		return true, fmt.Errorf("account unavailable")
	}
	if reenroll {
		return true, errNativeRecoveryRequired
	}
	if h.hasMFA(ctx, id) {
		raw, err := h.authChallenge(ctx, id, "login", map[string]string{"method": method})
		if err != nil {
			return true, err
		}
		h.setPendingAuth(w, raw)
		w.Header().Set("X-Calnode-Auth-Step", "mfa")
		return true, nil
	}
	if h.mfaRequired(ctx, id) {
		err := h.createSessionMethod(context.WithValue(ctx, ctxAuthSetup, true), w, id, method)
		w.Header().Set("X-Calnode-Auth-Step", "enroll")
		return true, err
	}
	return false, nil
}
func (h *Handler) consumeAuthChallenge(ctx context.Context, raw, purpose string) (string, string, error) {
	var id, payload string
	err := h.db.QueryRowContext(ctx, `DELETE FROM auth_challenges WHERE token_hash=? AND purpose=? AND expires_at>? RETURNING COALESCE(user_id,''),payload`, authsecurity.Hash(raw), purpose, time.Now().UTC().Format(time.RFC3339Nano)).Scan(&id, &payload)
	return id, payload, err
}
func (h *Handler) verifyNativeCode(ctx context.Context, id, code string) bool {
	// A new login challenge must not reset the account's OTP/recovery-code budget.
	if err := abuse.Take(ctx, h.db, abuse.Rule{Key: "mfa:account:" + id, Limit: 10, Window: 5 * time.Minute}); err != nil {
		h.logger.WarnContext(ctx, "MFA verification throttled or unavailable", "error", err)
		return false
	}

	code = strings.TrimSpace(code)
	if len(code) == 6 {
		var encrypted string
		if h.db.QueryRowContext(ctx, `SELECT secret_enc FROM user_totp WHERE user_id=? AND confirmed=1`, id).Scan(&encrypted) != nil {
			return false
		}
		seed, err := secret.Decrypt(h.encKey, encrypted)
		if err != nil {
			return false
		}
		step, ok := totpCodeStep(seed, code)
		if !ok {
			return false
		}
		res, e := h.db.ExecContext(ctx, `UPDATE user_totp SET last_step=? WHERE user_id=? AND confirmed=1 AND last_step<?`, step, id, step)
		if e != nil {
			return false
		}
		n, _ := res.RowsAffected()
		return n == 1
	}
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(code, "-", ""), " ", ""))
	if len(normalized) != 32 {
		return false
	}
	res, err := h.db.ExecContext(ctx, `DELETE FROM auth_recovery_codes WHERE user_id=? AND code_hash=?`, id, authsecurity.Hash(normalized))
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
}
func totpCodeStep(seed, code string) (int64, bool) {
	now := time.Now().UTC().Unix() / 30
	for _, step := range []int64{now, now - 1, now + 1} {
		candidate, err := totp.GenerateCodeCustom(seed, time.Unix(step*30, 0), totp.ValidateOpts{Period: 30, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
		if err == nil && subtle.ConstantTimeCompare([]byte(candidate), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}
func (h *Handler) VerifyNativeMFA(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	var in struct {
		Code string `json:"code"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		h.writeError(w, 400, "invalid JSON")
		return
	}
	c, err := r.Cookie(pendingAuthCookie)
	if err != nil {
		h.writeError(w, 401, "sign in again")
		return
	}
	var id, payload string
	err = h.db.QueryRowContext(r.Context(), `UPDATE auth_challenges SET attempts=attempts+1 WHERE token_hash=? AND purpose='login' AND attempts<5 AND expires_at>? RETURNING user_id,payload`, authsecurity.Hash(c.Value), time.Now().UTC().Format(time.RFC3339Nano)).Scan(&id, &payload)
	if err != nil || !h.passwordAllowed(r.Context(), id) || !h.verifyNativeCode(r.Context(), id, in.Code) {
		h.writeError(w, 401, "invalid or expired authentication code")
		return
	}
	if _, _, err = h.consumeAuthChallenge(r.Context(), c.Value, "login"); err != nil {
		h.writeError(w, 401, "sign in again")
		return
	}
	var p struct {
		Method string `json:"method"`
	}
	if json.Unmarshal([]byte(payload), &p) != nil {
		h.writeError(w, 401, "sign in again")
		return
	}
	if err = h.createSessionMethod(context.WithValue(r.Context(), ctxMFAVerified, true), w, id, p.Method); err != nil {
		h.writeError(w, 401, "sign in again")
		return
	}
	h.clearPendingAuth(w)
	h.writeJSON(w, 200, map[string]bool{"ok": true})
}
func (h *Handler) GetAuthSecurity(w http.ResponseWriter, r *http.Request) {
	u, _ := userFromContext(r.Context())
	w.Header().Set("Cache-Control", "no-store")
	var totpEnabled bool
	var count int
	_ = h.db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM user_totp WHERE user_id=? AND confirmed=1)`, u.ID).Scan(&totpEnabled)
	_ = h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM auth_recovery_codes WHERE user_id=?`, u.ID).Scan(&count)
	type key struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Created string `json:"created_at"`
	}
	keys := []key{}
	rows, err := h.db.QueryContext(r.Context(), `SELECT id,name,created_at FROM passkeys WHERE user_id=? ORDER BY created_at`, u.ID)
	if err != nil {
		h.writeError(w, 500, "could not load authentication methods")
		return
	}
	for rows.Next() {
		var k key
		if err = rows.Scan(&k.ID, &k.Name, &k.Created); err != nil {
			break
		}
		keys = append(keys, k)
	}
	scanErr := rows.Err()
	rows.Close()
	if err != nil || scanErr != nil {
		h.writeError(w, 500, "could not load authentication methods")
		return
	}
	h.writeJSON(w, 200, map[string]any{"totp_enabled": totpEnabled, "passkeys": keys, "recovery_codes_remaining": count, "setup_required": u.RecoveryOnly, "native_allowed": h.nativeEnrollmentAllowed(r.Context(), u), "mfa_required": h.mfaRequired(r.Context(), u.ID)})
}

// freshAuthFactor protects enrollment changes. An existing factor or recovery code
// must be verified; a password by itself cannot silently replace MFA.
func (h *Handler) freshAuthFactor(w http.ResponseWriter, r *http.Request, u AuthUser) bool {
	if u.CredentialID != "" || u.ImpersonatorID != "" || u.SessionID == "" {
		h.writeError(w, 403, "use your own interactive session")
		return false
	}
	if u.RecoveryOnly {
		return true
	}
	var mfa, verified, created, method string
	if h.db.QueryRowContext(r.Context(), `SELECT COALESCE(mfa_verified_at,''),COALESCE(reauthenticated_at,''),created_at,auth_method FROM sessions WHERE id=?`, u.SessionID).Scan(&mfa, &verified, &created, &method) != nil {
		h.writeError(w, 401, "sign in again")
		return false
	}
	fresh := func(s string) bool {
		at, e := time.Parse(time.RFC3339Nano, s)
		return e == nil && time.Since(at) >= 0 && time.Since(at) < 5*time.Minute
	}
	if h.hasMFA(r.Context(), u.ID) {
		if fresh(mfa) {
			return true
		}
		h.writeError(w, 403, "verify a passkey, authenticator code, or recovery code first")
		return false
	}
	if fresh(verified) || (isSSOMethod(method) && fresh(created)) {
		return true
	}
	h.writeError(w, 403, "verify your identity before changing authentication methods")
	return false
}
func (h *Handler) BeginTOTP(w http.ResponseWriter, r *http.Request) {
	u, _ := userFromContext(r.Context())
	if !h.nativeEnrollmentAllowed(r.Context(), u) {
		h.writeError(w, 403, "native authentication is disabled for this account")
		return
	}
	if !h.freshAuthFactor(w, r, u) {
		return
	}
	k, err := totp.Generate(totp.GenerateOpts{Issuer: "Calnode", AccountName: u.Email, SecretSize: 20})
	if err != nil {
		h.writeError(w, 500, "could not create authenticator setup")
		return
	}
	enc, err := secret.Encrypt(h.encKey, k.Secret())
	if err != nil {
		h.writeError(w, 500, "could not encrypt authenticator")
		return
	}
	token, err := h.authChallenge(r.Context(), u.ID, "totp:"+u.SessionID, map[string]string{"secret": enc})
	if err != nil {
		h.writeError(w, 500, "could not create authenticator setup")
		return
	}
	img, err := k.Image(220, 220)
	if err != nil {
		h.writeError(w, 500, "could not create QR code")
		return
	}
	var out bytes.Buffer
	if err = png.Encode(&out, img); err != nil {
		h.writeError(w, 500, "could not create QR code")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	h.writeJSON(w, 200, map[string]string{"setup_token": token, "secret": k.Secret(), "qr": "data:image/png;base64," + base64.StdEncoding.EncodeToString(out.Bytes())})
}
func (h *Handler) ConfirmTOTP(w http.ResponseWriter, r *http.Request) {
	u, _ := userFromContext(r.Context())
	if !h.nativeEnrollmentAllowed(r.Context(), u) {
		h.writeError(w, 403, "native authentication is disabled")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var in struct {
		Token string `json:"setup_token"`
		Code  string `json:"code"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		h.writeError(w, 400, "invalid JSON")
		return
	}
	var payload string
	err := h.db.QueryRowContext(r.Context(), `UPDATE auth_challenges SET attempts=attempts+1 WHERE token_hash=? AND user_id=? AND purpose=? AND expires_at>? AND attempts<5 RETURNING payload`, authsecurity.Hash(in.Token), u.ID, "totp:"+u.SessionID, time.Now().UTC().Format(time.RFC3339Nano)).Scan(&payload)
	var p struct {
		Secret string `json:"secret"`
	}
	if err != nil || json.Unmarshal([]byte(payload), &p) != nil {
		h.writeError(w, 400, "authenticator setup expired")
		return
	}
	seed, err := secret.Decrypt(h.encKey, p.Secret)
	step, valid := totpCodeStep(seed, in.Code)
	if err != nil || !valid {
		h.writeError(w, 400, "invalid authenticator code")
		return
	}
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.writeError(w, 500, "could not enroll authenticator")
		return
	}
	defer tx.Rollback() //nolint:errcheck
	res, err := tx.ExecContext(r.Context(), `DELETE FROM auth_challenges WHERE token_hash=? AND user_id=? AND purpose=?`, authsecurity.Hash(in.Token), u.ID, "totp:"+u.SessionID)
	if err != nil {
		h.writeError(w, 400, "setup expired")
		return
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		h.writeError(w, 400, "setup expired")
		return
	}
	_, err = tx.ExecContext(r.Context(), `INSERT INTO user_totp(user_id,secret_enc,confirmed,last_step)VALUES(?,?,1,?)`, u.ID, p.Secret, step)
	if err != nil || tx.Commit() != nil {
		h.writeError(w, 409, "remove the existing authenticator before replacing it")
		return
	}
	if !h.markMFAConfirmed(w, r, u) {
		return
	}
	h.securityNotice(r.Context(), u.ID, "Authenticator app enrolled", "")
	h.securityAudit(r.Context(), u.ID, "auth.totp.enroll", u.ID, nil)
	h.writeJSON(w, 200, map[string]bool{"ok": true})
}
func (h *Handler) markMFAConfirmed(w http.ResponseWriter, r *http.Request, u AuthUser) bool {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	h.revokeOtherSessions(r, u.ID)
	if _, err := h.db.ExecContext(r.Context(), `UPDATE users SET mfa_reenroll_required=0 WHERE id=?`, u.ID); err != nil {
		h.writeError(w, 500, "could not complete enrollment")
		return false
	}
	if u.RecoveryOnly {
		method := "password"
		var original string
		_ = h.db.QueryRowContext(r.Context(), `SELECT auth_method FROM sessions WHERE id=?`, u.SessionID).Scan(&original)
		if original == "server_recovery" {
			method = original
		}
		if err := h.createSessionMethod(context.WithValue(r.Context(), ctxMFAVerified, true), w, u.ID, method); err != nil {
			h.writeError(w, 500, "sign in again to finish setup")
			return false
		}
		_, _ = h.db.ExecContext(r.Context(), `DELETE FROM sessions WHERE id=?`, u.SessionID)
	} else {
		if _, err := h.db.ExecContext(r.Context(), `UPDATE sessions SET mfa_verified_at=?,reauthenticated_at=? WHERE id=?`, now, now, u.SessionID); err != nil {
			h.writeError(w, 500, "could not verify session")
			return false
		}
	}
	return true
}
func (h *Handler) GenerateRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	u, _ := userFromContext(r.Context())
	if !h.freshAuthFactor(w, r, u) {
		return
	}
	if !h.hasMFA(r.Context(), u.ID) {
		h.writeError(w, 400, "enroll an authentication method first")
		return
	}
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.writeError(w, 500, "could not generate recovery codes")
		return
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err = tx.ExecContext(r.Context(), `DELETE FROM auth_recovery_codes WHERE user_id=?`, u.ID); err != nil {
		h.writeError(w, 500, "could not replace recovery codes")
		return
	}
	codes := []string{}
	for i := 0; i < 10; i++ {
		var b [16]byte
		if _, err = rand.Read(b[:]); err != nil {
			h.writeError(w, 500, "could not generate recovery codes")
			return
		}
		s := hex.EncodeToString(b[:])
		if _, err = tx.ExecContext(r.Context(), `INSERT INTO auth_recovery_codes(code_hash,user_id)VALUES(?,?)`, authsecurity.Hash(s), u.ID); err != nil {
			h.writeError(w, 500, "could not save recovery codes")
			return
		}
		codes = append(codes, s[:8]+"-"+s[8:16]+"-"+s[16:24]+"-"+s[24:])
	}
	if tx.Commit() != nil {
		h.writeError(w, 500, "could not save recovery codes")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	h.securityNotice(r.Context(), u.ID, "Recovery codes replaced", "")
	h.writeJSON(w, 200, map[string]any{"codes": codes})
}
func (h *Handler) DeleteAuthFactor(w http.ResponseWriter, r *http.Request) {
	u, _ := userFromContext(r.Context())
	if !h.freshAuthFactor(w, r, u) {
		return
	}
	id := r.PathValue("id")
	required := h.mfaRequired(r.Context(), u.ID)
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.writeError(w, 500, "could not remove authentication method")
		return
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRowContext(r.Context(), `SELECT (SELECT COUNT(*) FROM passkeys WHERE user_id=?)+(SELECT COUNT(*) FROM user_totp WHERE user_id=? AND confirmed=1)`, u.ID, u.ID).Scan(&count); err != nil {
		h.writeError(w, 500, "could not load authentication methods")
		return
	}
	if count <= 1 && required {
		h.writeError(w, 400, "add a replacement before removing the last required authentication method")
		return
	}
	if id == "totp" {
		_, err = tx.ExecContext(r.Context(), `DELETE FROM user_totp WHERE user_id=?`, u.ID)
	} else {
		_, err = tx.ExecContext(r.Context(), `DELETE FROM passkeys WHERE id=? AND user_id=?`, id, u.ID)
	}
	if err != nil {
		h.writeError(w, 500, "could not remove authentication method")
		return
	}
	if count <= 1 {
		if _, err = tx.ExecContext(r.Context(), `DELETE FROM auth_recovery_codes WHERE user_id=?`, u.ID); err != nil {
			h.writeError(w, 500, "could not remove recovery codes")
			return
		}
	}
	if tx.Commit() != nil {
		h.writeError(w, 500, "could not remove authentication method")
		return
	}

	h.revokeOtherSessions(r, u.ID)
	h.securityNotice(r.Context(), u.ID, "Authentication method removed", "")
	h.securityAudit(r.Context(), u.ID, "auth.factor.remove", u.ID, map[string]string{"factor_id": id})
	h.writeJSON(w, 200, map[string]bool{"ok": true})
}
func (h *Handler) ResetUserMFA(w http.ResponseWriter, r *http.Request) {
	u, _ := userFromContext(r.Context())
	target := r.PathValue("id")
	var admin, owner bool
	if h.db.QueryRowContext(r.Context(), `SELECT is_admin,is_owner FROM users WHERE id=? AND archived_at IS NULL`, target).Scan(&admin, &owner) != nil {
		h.writeError(w, 404, "account not found")
		return
	}
	if owner || target == u.ID {
		h.writeError(w, 403, "use your backup authentication method or server recovery")
		return
	}
	if admin && !u.InstanceAdmin && !u.InstanceOwner {
		h.writeError(w, 403, "only an instance administrator can recover an administrator")
		return
	}
	if !h.passwordAllowed(r.Context(), target) {
		h.writeError(w, 403, "authentication recovery is managed by the SSO provider")
		return
	}
	if !h.freshAuthFactor(w, r, u) {
		return
	}
	if !h.hasMFA(r.Context(), u.ID) {
		var method string
		_ = h.db.QueryRowContext(r.Context(), `SELECT auth_method FROM sessions WHERE id=?`, u.SessionID).Scan(&method)
		if !isSSOMethod(method) {
			h.writeError(w, 403, "enroll and verify your own MFA before authorizing recovery")
			return
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	var in struct {
		Reason  string `json:"reason"`
		Disable bool   `json:"disable"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || len(strings.TrimSpace(in.Reason)) < 3 {
		h.writeError(w, 400, "record an identity-verification or support ticket reference")
		return
	}
	if in.Disable && h.mfaRequired(r.Context(), target) {
		h.writeError(w, 400, "MFA is required; use recovery and re-enrollment instead")
		return
	}
	token, err := authsecurity.Recover(r.Context(), h.db, target, u.ID, in.Reason, false, in.Disable)
	if err != nil {
		h.writeError(w, 500, "could not authorize recovery")
		return
	}
	url := ""
	if token != "" {
		url = strings.TrimRight(h.baseURL, "/") + "/admin/auth/recover#token=" + token
	}
	w.Header().Set("Cache-Control", "no-store")
	h.writeJSON(w, 200, map[string]string{"recovery_url": url})
}
func (h *Handler) ClaimAuthRecovery(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	var in struct {
		Token string `json:"token"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		h.writeError(w, 400, "invalid JSON")
		return
	}
	id, payload, err := h.consumeAuthChallenge(r.Context(), in.Token, "recovery")
	var recovery struct {
		Owner bool `json:"owner_recovery"`
	}
	_ = json.Unmarshal([]byte(payload), &recovery)
	var owner bool
	_ = h.db.QueryRowContext(r.Context(), `SELECT is_owner FROM users WHERE id=? AND archived_at IS NULL`, id).Scan(&owner)
	method := "password"
	if recovery.Owner && owner {
		method = "server_recovery"
	}
	if err != nil || (method != "server_recovery" && !h.passwordAllowed(r.Context(), id)) {
		h.writeError(w, 401, "recovery link is invalid, expired, or no longer allowed")
		return
	}
	if err = h.createSessionMethod(context.WithValue(r.Context(), ctxAuthSetup, true), w, id, method); err != nil {
		h.writeError(w, 401, "recovery could not start")
		return
	}
	h.writeJSON(w, 200, map[string]bool{"ok": true})
}
func (h *Handler) securityNotice(ctx context.Context, id, action, reason string) {
	b, _ := json.Marshal(map[string]string{"user_id": id, "action": action, "reason": reason})
	if _, err := h.db.ExecContext(ctx, `INSERT INTO jobs(id,type,payload,run_at)VALUES(?,'security.notify',?,?)`, uid.New(), string(b), time.Now().UTC().Format(time.RFC3339)); err != nil {
		h.logger.Error("security notification queue failed", "error", err)
	}
}
func (h *Handler) JobSecurityNotify(ctx context.Context, payload string) error {
	var p struct {
		ID     string `json:"user_id"`
		Action string `json:"action"`
		Reason string `json:"reason"`
	}
	if json.Unmarshal([]byte(payload), &p) != nil {
		return fmt.Errorf("invalid security notification")
	}
	if !h.isEmailEnabled() {
		return nil
	}
	rows, err := h.db.QueryContext(ctx, `SELECT DISTINCT email FROM users WHERE archived_at IS NULL AND (id=? OR is_admin=1 OR is_owner=1)`, p.ID)
	if err != nil {
		return err
	}
	var emails []string
	for rows.Next() {
		var email string
		if err = rows.Scan(&email); err != nil {
			break
		}
		emails = append(emails, email)
	}
	scanErr := rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if scanErr != nil {
		return scanErr
	}
	for _, email := range emails {
		if err = h.mailer.Send(ctx, mailer.Message{To: []string{email}, Subject: "Calnode security notification", Text: p.Action + ".\nAccount: " + p.ID + "\nReference: " + p.Reason + "\nReview Settings → Members & Roles if this was unexpected."}); err != nil {
			return err
		}
	}
	return nil
}

// Session policy is enforced on every request, including MCP consent. A session
// created before MFA enrollment/enforcement cannot retain password-only access.
func (h *Handler) sessionSecurityValid(ctx context.Context, sessionID, userID, method string, p SigninPolicy, setup bool) bool {
	var created, mfa string
	var owner bool
	if h.db.QueryRowContext(ctx, `SELECT s.created_at,COALESCE(s.mfa_verified_at,''),u.is_owner FROM sessions s JOIN users u ON u.id=? WHERE s.id=? AND (s.user_id=? OR s.impersonator_user_id=?) AND u.archived_at IS NULL`, userID, sessionID, userID, userID).Scan(&created, &mfa, &owner) != nil {
		return false
	}
	at, err := time.Parse(time.RFC3339Nano, created)
	hours := p.SessionHours
	if hours <= 0 {
		hours = 12
	}
	if err != nil || time.Since(at) < 0 || time.Since(at) > time.Duration(hours)*time.Hour {
		return false
	}
	if method == "server_recovery" && (!owner || time.Since(at) > time.Hour) {
		return false
	}
	if isSSOMethod(method) {
		return !setup && h.providerSessionAllowed(ctx, method, userID)
	}
	if setup {
		return true
	}
	return mfa != "" || (!h.hasMFA(ctx, userID) && !h.mfaRequired(ctx, userID))
}

func (h *Handler) impersonationSessionValid(ctx context.Context, u AuthUser, method string, policy SigninPolicy) bool {
	grants, err := access.Load(ctx, h.db, u.ImpersonatorID)
	if err != nil || !grants.Has("impersonation.use") || u.IsOwner {
		return false
	}
	var targetAllows bool
	if h.db.QueryRowContext(ctx, `SELECT COALESCE(r.is_impersonatable,1) FROM users target LEFT JOIN roles r ON r.id=target.custom_role_id WHERE target.id=? AND target.archived_at IS NULL`, u.ID).Scan(&targetAllows) != nil || !targetAllows {
		return false
	}
	var admin bool
	if h.db.QueryRowContext(ctx, `SELECT (is_admin=1 OR is_owner=1) FROM users WHERE id=? AND archived_at IS NULL`, u.ImpersonatorID).Scan(&admin) != nil || (u.IsAdmin && !admin) {
		return false
	}
	return policyAllowsSession(policy, method, admin) && h.sessionSecurityValid(ctx, u.SessionID, u.ImpersonatorID, method, policy, false)
}

func (h *Handler) nativeEnrollmentAllowed(ctx context.Context, u AuthUser) bool {
	if h.passwordAllowed(ctx, u.ID) {
		return true
	}
	if !u.RecoveryOnly || !u.InstanceOwner {
		return false
	}
	var method string
	return h.db.QueryRowContext(ctx, `SELECT auth_method FROM sessions WHERE id=? AND user_id=?`, u.SessionID, u.ID).Scan(&method) == nil && method == "server_recovery"
}
