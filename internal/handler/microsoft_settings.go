package handler

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/calnode/calnode/internal/calendar/microsoft"
	"github.com/calnode/calnode/internal/secret"
)

// MicrosoftOAuthConfig holds decrypted Microsoft OAuth settings loaded from the DB.
type MicrosoftOAuthConfig struct {
	ClientID               string
	ClientSecret           string
	TenantID               string
	AllowedSignupDomains   []string
	AllowPersonalCalendars bool
	CalClientID            string
	CalClientSecret        string
}

func parseDomainsCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		d := strings.TrimSpace(strings.ToLower(part))
		if d != "" {
			out = append(out, d)
		}
	}
	return out
}

// LoadMicrosoftSettingsFromDB reads Microsoft OAuth credentials from server_settings.
// Returns nil (not an error) when microsoft_client_id is empty.
func LoadMicrosoftSettingsFromDB(db *sql.DB, encKey [32]byte) (*MicrosoftOAuthConfig, error) {
	var clientID, secretEnc, tenantID, domainsCSV string
	var allowPersonal int
	var calClientID, calSecretEnc string
	err := db.QueryRow(`
		SELECT microsoft_client_id, microsoft_client_secret_enc, microsoft_tenant_id, microsoft_allowed_signup_domains,
		       microsoft_allow_personal_calendars, microsoft_cal_client_id, microsoft_cal_client_secret_enc
		FROM server_settings WHERE id = 1`).
		Scan(&clientID, &secretEnc, &tenantID, &domainsCSV, &allowPersonal, &calClientID, &calSecretEnc)
	if err == sql.ErrNoRows || clientID == "" {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var clientSecret string
	if secretEnc != "" {
		clientSecret, err = secret.Decrypt(encKey, secretEnc)
		if err != nil {
			return nil, fmt.Errorf("decrypt microsoft client secret: %w", err)
		}
	}
	var calClientSecret string
	if calSecretEnc != "" {
		calClientSecret, err = secret.Decrypt(encKey, calSecretEnc)
		if err != nil {
			return nil, fmt.Errorf("decrypt microsoft calendar client secret: %w", err)
		}
	}
	return &MicrosoftOAuthConfig{
		ClientID:               strings.TrimSpace(clientID),
		ClientSecret:           clientSecret,
		TenantID:               strings.TrimSpace(tenantID),
		AllowedSignupDomains:   parseDomainsCSV(domainsCSV),
		AllowPersonalCalendars: allowPersonal != 0,
		CalClientID:            strings.TrimSpace(calClientID),
		CalClientSecret:        calClientSecret,
	}, nil
}

// GetMicrosoftSettings handles GET /v1/settings/microsoft (admin only).
func (h *Handler) GetMicrosoftSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	var clientID, secretEnc, tenantID, domainsCSV string
	var allowPersonal int
	var calClientID, calSecretEnc string
	err := h.db.QueryRowContext(r.Context(), `
		SELECT microsoft_client_id, microsoft_client_secret_enc, microsoft_tenant_id, microsoft_allowed_signup_domains,
		       microsoft_allow_personal_calendars, microsoft_cal_client_id, microsoft_cal_client_secret_enc
		FROM server_settings WHERE id = 1`).
		Scan(&clientID, &secretEnc, &tenantID, &domainsCSV, &allowPersonal, &calClientID, &calSecretEnc)
	if err != nil && err != sql.ErrNoRows {
		h.logger.ErrorContext(r.Context(), "microsoft settings: query", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"client_id":                clientID,
		"client_secret_set":        secretEnc != "",
		"tenant_id":                tenantID,
		"allowed_signup_domains":   domainsCSV,
		"allow_personal_calendars": allowPersonal != 0,
		"cal_client_id":            calClientID,
		"cal_client_secret_set":    calSecretEnc != "",
		"configured":               clientID != "" && secretEnc != "",
		"base_url":                 h.baseURL,
	})
}

// PutMicrosoftSettings handles PUT and PATCH /v1/settings/microsoft (admin only).
func (h *Handler) PutMicrosoftSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	if h.demoMode {
		h.writeError(w, http.StatusServiceUnavailable, "not available in the demo")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var req struct {
		ClientID               string `json:"client_id"`
		ClientSecret           string `json:"client_secret"`
		TenantID               string `json:"tenant_id"`
		AllowedSignupDomains   string `json:"allowed_signup_domains"`
		AllowPersonalCalendars bool   `json:"allow_personal_calendars"`
		CalClientID            string `json:"cal_client_id"`
		CalClientSecret        string `json:"cal_client_secret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	req.ClientID = strings.TrimSpace(req.ClientID)
	req.TenantID = strings.TrimSpace(req.TenantID)
	req.AllowedSignupDomains = strings.TrimSpace(req.AllowedSignupDomains)
	req.CalClientID = strings.TrimSpace(req.CalClientID)
	allowPersonalInt := 0
	if req.AllowPersonalCalendars {
		allowPersonalInt = 1
	}

	if req.ClientID == "" {
		if _, err := h.db.ExecContext(r.Context(), `
			UPDATE server_settings SET
			  microsoft_client_id = '',
			  microsoft_client_secret_enc = '',
			  microsoft_tenant_id = '',
			  microsoft_allowed_signup_domains = ?,
			  microsoft_allow_personal_calendars = 0,
			  microsoft_cal_client_id = '',
			  microsoft_cal_client_secret_enc = '',
			  updated_at = datetime('now')
			WHERE id = 1`, req.AllowedSignupDomains); err != nil {
			h.logger.ErrorContext(r.Context(), "microsoft settings: clear", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		h.SetAllowedSignupDomains(parseDomainsCSV(req.AllowedSignupDomains))
		h.SetMicrosoftAuth("", "", "common", "", h.secureCookie)
		h.GetMicrosoftSettings(w, r)
		return
	}

	var existingEnc, existingCalEnc string
	_ = h.db.QueryRowContext(r.Context(), `SELECT microsoft_client_secret_enc, microsoft_cal_client_secret_enc FROM server_settings WHERE id = 1`).
		Scan(&existingEnc, &existingCalEnc)

	secretEnc := existingEnc
	clientSecret := ""
	if req.ClientSecret != "" {
		var err error
		secretEnc, err = secret.Encrypt(h.encKey, req.ClientSecret)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "microsoft settings: encrypt secret", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		clientSecret = req.ClientSecret
	} else if existingEnc != "" {
		var err error
		clientSecret, err = secret.Decrypt(h.encKey, existingEnc)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "microsoft settings: decrypt existing secret", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	calSecretEnc := existingCalEnc
	calClientSecret := ""
	if req.CalClientSecret != "" {
		var err error
		calSecretEnc, err = secret.Encrypt(h.encKey, req.CalClientSecret)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "microsoft settings: encrypt calendar secret", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		calClientSecret = req.CalClientSecret
	} else if existingCalEnc != "" {
		var err error
		calClientSecret, err = secret.Decrypt(h.encKey, existingCalEnc)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "microsoft settings: decrypt calendar secret", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	if _, err := h.db.ExecContext(r.Context(), `
		UPDATE server_settings SET
		  microsoft_client_id = ?,
		  microsoft_client_secret_enc = ?,
		  microsoft_tenant_id = ?,
		  microsoft_allowed_signup_domains = ?,
		  microsoft_allow_personal_calendars = ?,
		  microsoft_cal_client_id = ?,
		  microsoft_cal_client_secret_enc = ?,
		  updated_at = datetime('now')
		WHERE id = 1`,
		req.ClientID, secretEnc, req.TenantID, req.AllowedSignupDomains,
		allowPersonalInt, req.CalClientID, calSecretEnc); err != nil {
		h.logger.ErrorContext(r.Context(), "microsoft settings: save", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	tenant := req.TenantID
	if tenant == "" {
		tenant = "common"
	}
	h.SetAllowedSignupDomains(parseDomainsCSV(req.AllowedSignupDomains))

	if req.ClientID != "" && clientSecret != "" {
		authRedirect := strings.TrimRight(h.baseURL, "/") + "/v1/auth/microsoft/callback"
		calRedirect := strings.TrimRight(h.baseURL, "/") + "/v1/calendar/microsoft/callback"
		h.SetMicrosoftAuth(req.ClientID, clientSecret, tenant, authRedirect, h.secureCookie)
		encKeyHex := hex.EncodeToString(h.encKey[:])
		calID, calSecret, calTenant := req.ClientID, clientSecret, tenant
		if req.AllowPersonalCalendars && req.CalClientID != "" && calClientSecret != "" {
			calID, calSecret, calTenant = req.CalClientID, calClientSecret, "common"
		}
		if mc, err := microsoft.New(h.db, calID, calSecret, calTenant, calRedirect, encKeyHex); err == nil {
			if h.cal != nil {
				h.cal.Register(mc)
			}
		}
	}

	h.GetMicrosoftSettings(w, r)
}
