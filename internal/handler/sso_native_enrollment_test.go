package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func TestSSOOnlyMemberCannotEnrollNativeCredentials(t *testing.T) {
	h := securitySeed(t)
	setTestSigninPolicy(t, h, SigninPolicy{MembersSSOOnly: true, MicrosoftEnabled: true, AdminPasswordEnabled: true, SessionHours: 12})
	session := httptest.NewRecorder()
	if err := h.createSessionMethod(context.Background(), session, "member-a", "microsoft"); err != nil {
		t.Fatal(err)
	}
	cookie := securityCookie(t, session, sessionCookieName)
	status := securityCall(h, h.GetAuthSecurity, cookie, "GET", "/v1/users/me/security", "", nil)
	var body struct {
		NativeAllowed bool `json:"native_allowed"`
	}
	if status.Code != http.StatusOK || json.Unmarshal(status.Body.Bytes(), &body) != nil || body.NativeAllowed {
		t.Fatalf("SSO member was offered native enrollment: %d %s", status.Code, status.Body)
	}
	for name, handler := range map[string]http.HandlerFunc{
		"password": h.ChangePassword, "totp-begin": h.BeginTOTP, "totp-confirm": h.ConfirmTOTP,
		"passkey-begin": h.BeginPasskeyRegistration, "passkey-finish": h.FinishPasskeyRegistration,
	} {
		t.Run(name, func(t *testing.T) {
			response := securityCall(h, handler, cookie, "POST", "/", `{}`, nil)
			if response.Code != http.StatusForbidden {
				t.Fatalf("native credential operation escaped SSO policy: %d %s", response.Code, response.Body)
			}
		})
	}
	var factors int
	if err := h.db.QueryRow(`SELECT (SELECT COUNT(*) FROM user_totp WHERE user_id='member-a')+(SELECT COUNT(*) FROM passkeys WHERE user_id='member-a')+(SELECT COUNT(*) FROM auth_challenges WHERE user_id='member-a')`).Scan(&factors); err != nil || factors != 0 {
		t.Fatalf("rejected native enrollment persisted credentials/challenges: %d, %v", factors, err)
	}
}

func TestSSOPolicyChangeBlocksFinishingEarlierNativeEnrollment(t *testing.T) {
	h := securitySeed(t)
	policy := SigninPolicy{MicrosoftEnabled: true, AdminPasswordEnabled: true, SessionHours: 12}
	setTestSigninPolicy(t, h, policy)
	session := httptest.NewRecorder()
	if err := h.createSessionMethod(context.Background(), session, "member-a", "microsoft"); err != nil {
		t.Fatal(err)
	}
	cookie := securityCookie(t, session, sessionCookieName)
	begin := securityCall(h, h.BeginTOTP, cookie, "POST", "/", `{}`, nil)
	var setup struct {
		Token  string `json:"setup_token"`
		Secret string `json:"secret"`
	}
	if begin.Code != http.StatusOK || json.Unmarshal(begin.Body.Bytes(), &setup) != nil {
		t.Fatalf("company-allowed enrollment failed: %d %s", begin.Code, begin.Body)
	}
	policy.MembersSSOOnly = true
	setTestSigninPolicy(t, h, policy)
	code, err := totp.GenerateCode(setup.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]string{"setup_token": setup.Token, "code": code})
	response := securityCall(h, h.ConfirmTOTP, cookie, "POST", "/", string(payload), nil)
	if response.Code != http.StatusForbidden {
		t.Fatalf("earlier enrollment bypassed newly enforced SSO: %d %s", response.Code, response.Body)
	}
	var confirmed int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM user_totp WHERE user_id='member-a' AND confirmed=1`).Scan(&confirmed); err != nil || confirmed != 0 {
		t.Fatalf("SSO member acquired a confirmed native factor: %d, %v", confirmed, err)
	}
}
