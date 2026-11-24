package handler

// The operation registry deliberately reuses the authenticated REST handlers. It is
// not an arbitrary URL proxy: callers can invoke only these named operations.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/access"
	"github.com/calnode/calnode/internal/uid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type agentOperation struct {
	Name    string            `json:"name" jsonschema:"Exact operation name from list_agent_operations"`
	Target  string            `json:"target,omitempty" jsonschema:"Event slug or resource ID as described by the operation"`
	ChildID string            `json:"child_id,omitempty" jsonschema:"Question ID for question operations"`
	Values  map[string]any    `json:"values,omitempty" jsonschema:"JSON fields accepted by the existing REST operation; no passwords or credentials"`
	Query   map[string]string `json:"query,omitempty" jsonschema:"Optional list filters"`
}
type plannedOperation struct {
	agentOperation
	Before      any    `json:"before,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
}
type agentOperationSpec struct {
	method, path, description string
	handler                   http.HandlerFunc
	sensitive                 bool
	before                    string
}

func (h *Handler) agentOperations() map[string]agentOperationSpec {
	ops := map[string]agentOperationSpec{}
	add := func(name, method, path, description string, fn http.HandlerFunc, sensitive bool, before string) {
		ops[name] = agentOperationSpec{method, path, description, fn, sensitive, before}
	}
	add("list_event_type_configurations", "GET", "/v1/event-types", "List authorized personal and team event types, including inactive/private configurations. Query scope=all requests all authorized entries.", h.ListEventTypes, false, "")
	add("get_event_type_configuration", "GET", "/v1/event-types/{slug}", "Full editable event configuration. Target: event slug. Use this to map exports before creating an inactive/private replacement.", h.GetEventType, false, "")
	add("create_event_type", "POST", "/v1/event-types", "Create a reusable event. Values include slug, name, duration_minutes, location_type, location_value, team_id, routing_mode and booking policies. Imported events are inactive/private until explicitly published in a separate update.", h.CreateEventType, true, "")
	add("import_event_type", "POST", "/v1/event-types", "Import one event configuration using the combined create/update fields. Reuses the existing event validators; always inactive/private. Requires both create and update permissions. Advanced validation failures leave an explicitly reported inactive draft. Does not import bookings, credentials, host assignments or questions.", h.importAgentEventType, true, "")
	add("update_event_type", "PATCH", "/v1/event-types/{slug}", "Change reusable event settings. Target: event slug. Values use the fields returned by get_event_type_configuration. Changes may affect future bookings.", h.PatchEventType, true, "get_event_type_configuration")
	add("delete_event_type", "DELETE", "/v1/event-types/{slug}", "Delete a reusable event type. Target: event slug.", h.DeleteEventType, true, "get_event_type_configuration")
	add("duplicate_event_type", "POST", "/v1/event-types/{slug}/duplicate", "Duplicate an event type. Target: source slug. Values: name, slug.", h.DuplicateEventType, true, "get_event_type_configuration")
	add("get_event_hosts", "GET", "/v1/event-types/{slug}/hosts", "Read assigned hosts. Target: event slug.", h.ListEventTypeHosts, false, "")
	add("set_event_hosts", "PUT", "/v1/event-types/{slug}/hosts", "Configure reusable event hosts using the same hosts payload as the event editor.", h.SetEventTypeHosts, true, "get_event_hosts")
	add("list_event_questions", "GET", "/v1/event-types/{slug}/questions/admin", "Read event intake questions.", h.ListQuestionsAdmin, false, "")
	add("create_event_question", "POST", "/v1/event-types/{slug}/questions", "Create an intake question using the existing question schema.", h.CreateQuestion, true, "list_event_questions")
	add("update_event_question", "PATCH", "/v1/event-types/{slug}/questions/{id}", "Edit an intake question. Target: event slug; child_id: question ID.", h.UpdateQuestion, true, "list_event_questions")
	add("delete_event_question", "DELETE", "/v1/event-types/{slug}/questions/{id}", "Delete an intake question. Target: event slug; child_id: question ID.", h.DeleteQuestion, true, "list_event_questions")
	add("create_one_time_link", "POST", "/v1/event-types/{id}/single-use-links", "Create a one-booking link. Target: event ID or slug. Optional expires_in_hours, or expires_in_seconds (1–604800); mutually exclusive. Optional prefill maps name, email or existing question identifiers to saved form defaults. Does not change the reusable event.", h.CreateSingleUseLink, false, "")
	add("offer_custom_times", "POST", "/v1/event-types/{id}/single-use-links", "Offer 1–20 custom_slots, each with start_at/end_at RFC3339. Bypasses weekly hours/minimum notice; live busy conflicts still apply. Target: event ID or slug.", h.CreateSingleUseLink, false, "")
	add("list_one_time_links", "GET", "/v1/event-types/{id}/single-use-links", "Read authorized event links. Target: event ID or slug.", h.ListSingleUseLinks, false, "")
	add("revoke_one_time_link", "DELETE", "/v1/single-use-links/{token}", "Revoke an unused link. Target: its token.", h.RevokeSingleUseLink, false, "")
	add("get_dynamic_link_options", "GET", "/v1/dynamic-links/options", "List eligible people and available organizer options for a one-time meeting.", h.DynamicLinkOptions, false, "")
	add("create_dynamic_link", "POST", "/v1/dynamic-links", "Create a one-time group link with host_ids and optional organizer_id, name, duration_minutes, duration_options, location_type, location_value, destination_id. Does not change anyone's defaults.", h.CreateDynamicLink, false, "")
	add("get_my_preferences", "GET", "/v1/users/me", "Read your profile and preferences. Does not return credentials.", h.GetMe, false, "")
	add("update_my_preferences", "PATCH", "/v1/users/me", "Update profile/preferences using existing profile fields. Passwords, identity and MFA are not supported here.", h.PatchMe, true, "get_my_preferences")
	add("get_meeting_defaults", "GET", "/v1/users/me/meeting-defaults", "Read your default conferencing and booking preferences.", h.GetMeetingDefaults, false, "")
	add("update_meeting_defaults", "PUT", "/v1/users/me/meeting-defaults", "Set personal meeting defaults. Calendar connections must already be authorized.", h.PutMeetingDefaults, true, "get_meeting_defaults")
	add("list_teams", "GET", "/v1/teams", "List teams visible to you.", h.ListTeams, false, "")
	add("get_team", "GET", "/v1/teams/{id}", "Read a team. Target: team ID.", h.GetTeam, false, "")
	add("create_team", "POST", "/v1/teams", "Create a team. Requires instance authority.", h.CreateTeam, true, "")
	add("update_team", "PATCH", "/v1/teams/{id}", "Update team branding, description and permitted routing settings. Requires current head/admin authority.", h.PatchTeam, true, "get_team")
	add("delete_team", "DELETE", "/v1/teams/{id}", "Delete a team. Requires instance authority.", h.DeleteTeam, true, "get_team")
	add("get_branding", "GET", "/v1/settings/branding", "Read instance branding.", h.GetBranding, false, "")
	add("update_branding", "PATCH", "/v1/settings/branding", "Update branding settings.", h.PatchBranding, true, "get_branding")
	add("get_calendar_connections", "GET", "/v1/calendar/status", "Read your calendar connection capabilities without credentials.", h.CalendarStatus, false, "")
	add("list_booking_calendars", "GET", "/v1/calendar/calendars", "List calendars exposed by your authorized connections.", h.ListConnectedCalendars, false, "")
	add("set_booking_calendar", "PUT", "/v1/calendar/connections/{id}/destination", "Choose your default connected booking account. Target: connection ID. Values: provider, account_email. Does not connect an account or change conflict checks.", h.SetCalendarDestination, true, "get_calendar_connections")
	add("get_integration_providers", "GET", "/v1/settings/identity-providers", "Read redacted identity/integration configuration. Requires settings.read.", h.ListIdentityProviders, false, "")
	add("draft_identity_provider", "POST", "/v1/agent/integration-drafts", "Prepare a disabled Entra/Google/OIDC provider. Values match the provider form; secrets are forbidden. Human must supply the secret and activate in Identity & integrations.", h.createAgentIntegrationDraft, true, "")
	add("get_application_logs", "GET", "/v1/agent/application-logs", "Read bounded application delivery/job status and agent activity. Raw container stdout and secret-bearing provider errors are not exposed.", h.agentApplicationLogs, false, "")
	add("get_audit_logs", "GET", "/v1/security/audit", "Read redacted security audit entries. Requires logs.read.", h.ListSecurityAudit, false, "")
	// Settings remain in their original handlers, including provider validation.
	for _, x := range []struct {
		name       string
		get, patch http.HandlerFunc
	}{
		{"email", h.GetEmailSettings, h.PatchEmailSettings}, {"zoom", h.GetZoomSettings, h.PatchZoomSettings},
		{"video", h.GetLiveKitSettings, h.PatchLiveKitSettings}, {"storage", h.GetStorageSettings, h.PatchStorageSettings},
		{"notetaker", h.GetNotetakerSettings, h.PatchNotetakerSettings}, {"stripe", h.GetStripeSettings, h.PatchStripeSettings},
		{"tracking", h.GetTrackingSettings, h.PatchTrackingSettings}, {"llm", h.GetLLMSettings, h.PatchLLMSettings},
	} {
		add("get_"+x.name+"_settings", "GET", "/v1/settings/"+x.name, "Read redacted "+x.name+" configuration.", x.get, false, "")
		add("update_"+x.name+"_settings", "PATCH", "/v1/settings/"+x.name, "Update non-secret "+x.name+" configuration. Credentials require the human UI.", x.patch, true, "get_"+x.name+"_settings")
	}
	for _, x := range []struct {
		name, path                string
		list, create, update, del http.HandlerFunc
	}{
		{"availability_rule", "availability-rules", h.ListAvailabilityRules, h.CreateAvailabilityRule, h.UpdateAvailabilityRule, h.DeleteAvailabilityRule},
		{"availability_override", "availability-overrides", h.ListAvailabilityOverrides, h.CreateAvailabilityOverride, h.UpdateAvailabilityOverride, h.DeleteAvailabilityOverride},
		{"availability_schedule", "availability-schedules", h.ListAvailabilitySchedules, h.CreateAvailabilitySchedule, h.PatchAvailabilitySchedule, h.DeleteAvailabilitySchedule},
		{"out_of_office", "ooo", h.ListOOOEntries, h.CreateOOOEntry, h.UpdateOOOEntry, h.DeleteOOOEntry},
	} {
		add("list_"+x.name, "GET", "/v1/"+x.path, "Read authorized "+x.name+" records.", x.list, false, "")
		add("create_"+x.name, "POST", "/v1/"+x.path, "Create "+x.name+" using existing availability API fields.", x.create, true, "")
		add("update_"+x.name, "PATCH", "/v1/"+x.path+"/{id}", "Update "+x.name+". Target: record ID.", x.update, true, "list_"+x.name)
		add("delete_"+x.name, "DELETE", "/v1/"+x.path+"/{id}", "Delete "+x.name+". Target: record ID.", x.del, true, "list_"+x.name)
	}
	return ops
}

func operationPermission(spec agentOperationSpec) string {
	switch handlerName(spec.handler) {
	case "importAgentEventType":
		return "event_types.create"
	case "ListIdentityProviders":
		return "settings.read"
	case "createAgentIntegrationDraft":
		return "settings.update"
	case "agentApplicationLogs":
		return "logs.read"
	}
	return handlerPermissions[handlerName(spec.handler)]
}

func agentSecretField(key string) bool {
	key = strings.ToLower(key)
	if strings.HasSuffix(key, "_set") || strings.HasSuffix(key, "_configured") {
		return false
	}
	return strings.HasSuffix(key, "_pass") || key == "apikey" || strings.Contains(key, "privatekey") || strings.Contains(key, "password") || strings.Contains(key, "secret") || key == "api_key" || strings.HasSuffix(key, "_api_key") || key == "access_token" || key == "refresh_token" || strings.Contains(key, "private_key") || strings.HasSuffix(key, "_enc") || key == "authorization"
}
func redactAgentData(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, value := range x {
			if agentSecretField(k) {
				x[k] = "[redacted]"
			} else if k == "details" { // Audit details may contain arbitrary old payloads; expose metadata only.
				x[k] = "[details available in interactive audit view]"
			} else {
				x[k] = redactAgentData(value)
			}
		}
	case []any:
		for i := range x {
			x[i] = redactAgentData(x[i])
		}
	}
	return v
}
func rejectAgentSecrets(v any) error {
	switch x := v.(type) {
	case map[string]any:
		for k, v := range x {
			if agentSecretField(k) {
				return fmt.Errorf("%s requires human entry in the settings UI", k)
			}
			if err := rejectAgentSecrets(v); err != nil {
				return err
			}
		}
	case []any:
		for _, v := range x {
			if err := rejectAgentSecrets(v); err != nil {
				return err
			}
		}
	}
	return nil
}
func (h *Handler) operationRequest(ctx context.Context, op agentOperation, spec agentOperationSpec) (*http.Request, error) {
	path := spec.path
	if strings.Contains(path, "{") && (op.Target == "" || strings.ContainsAny(op.Target, "/?#\\") || op.Target == "." || op.Target == "..") {
		return nil, errors.New("valid target is required")
	}
	for _, key := range []string{"slug", "token", "id"} {
		value := op.Target
		if key == "id" && strings.Contains(path, "{slug}") {
			value = op.ChildID
		}
		path = strings.ReplaceAll(path, "{"+key+"}", url.PathEscape(value))
	}
	q := url.Values{}
	for k, v := range op.Query {
		if k != "user_id" {
			q.Set(k, v)
		}
	}
	raw, _ := json.Marshal(op.Values)
	if op.Values == nil {
		raw = []byte("{}")
	}
	r, _ := http.NewRequestWithContext(ctx, spec.method, path+"?"+q.Encode(), bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	r.SetPathValue("slug", op.Target)
	r.SetPathValue("id", op.Target)
	r.SetPathValue("token", op.Target)
	if strings.Contains(spec.path, "{slug}") && strings.Contains(spec.path, "{id}") {
		if op.ChildID == "" {
			return nil, errors.New("child_id is required")
		}
		r.SetPathValue("id", op.ChildID)
	}
	return r, nil
}
func (h *Handler) agentActor(ctx context.Context) (AuthUser, error) {
	c, ok := mcpCallerFromContext(ctx)
	if !ok || c.CredentialID == "" {
		return AuthUser{}, errors.New("connect through authenticated MCP to use management tools")
	}
	return h.currentAgentActor(ctx, c.UserID, c.CredentialID, c.Permissions)
}
func (h *Handler) currentAgentActor(ctx context.Context, userID, credentialID string, ceiling access.Grants) (AuthUser, error) {
	var u AuthUser
	err := h.db.QueryRowContext(ctx, `SELECT id,email,name,iana_timezone,is_admin,is_owner FROM users WHERE id=? AND archived_at IS NULL`, userID).Scan(&u.ID, &u.Email, &u.Name, &u.IANATZ, &u.IsAdmin, &u.IsOwner)
	if err != nil {
		return u, errors.New("user is no longer active")
	}
	grants, err := access.Load(ctx, h.db, userID)
	if err != nil {
		return u, err
	}
	var raw string
	err = h.db.QueryRowContext(ctx, `SELECT COALESCE(permissions_json,'{}') FROM oauth_access_tokens WHERE id=? AND user_id=? AND datetime(expires_at)>datetime('now') UNION ALL SELECT COALESCE(permissions_json,'{}') FROM api_keys WHERE id=? AND user_id=? AND (expires_at IS NULL OR datetime(expires_at)>datetime('now')) LIMIT 1`, credentialID, userID, credentialID, userID).Scan(&raw)
	if err != nil {
		return u, errors.New("connection revoked or expired")
	}
	u.Permissions = access.Intersect(access.Intersect(grants, access.Parse(raw)), ceiling)
	u.CredentialID = credentialID
	if !u.Permissions.Has("mcp.connect") {
		return u, errors.New("MCP access revoked")
	}
	u.InstanceAdmin = u.IsAdmin || u.IsOwner
	u.InstanceOwner = u.IsOwner
	u.IsOwner = false
	u.CanManageSettings = u.Permissions.Has("settings.update")
	u.CanManageTeams = u.Permissions.Has("teams.update")
	u.CanManageUsers = u.Permissions.Has("members.read")
	return u, nil
}
func (h *Handler) checkAgentOperation(ctx context.Context, u AuthUser, op agentOperation) (agentOperationSpec, error) {
	spec, ok := h.agentOperations()[op.Name]
	if !ok {
		return spec, errors.New("unknown operation")
	}
	if keys := agentFields(spec); keys != nil {
		for key := range op.Values {
			found := false
			for _, allowed := range keys {
				found = found || allowed == key
			}
			if !found {
				return spec, fmt.Errorf("unsupported field %s for %s; inspect list_agent_operations for supported fields", key, op.Name)
			}
		}
	}
	if err := rejectAgentSecrets(op.Values); err != nil {
		return spec, err
	}
	if op.Name == "create_one_time_link" && op.Values["custom_slots"] != nil {
		return spec, errors.New("use offer_custom_times for custom slots")
	}
	if op.Name == "offer_custom_times" && op.Values["custom_slots"] == nil {
		return spec, errors.New("custom_slots are required")
	}
	r, err := h.operationRequest(ctx, op, spec)
	if err != nil {
		return spec, err
	}
	p := operationPermission(spec)
	if handlerName(spec.handler) == "ListEventTypes" {
		p = access.EventListPermission(u.Permissions)
	}
	if strings.HasPrefix(p, "event_types.") {
		var target string
		name := handlerName(spec.handler)
		if name == "importAgentEventType" {
			name = "CreateEventType"
		}
		p, target, err = h.eventRequestPermission(r, p, name)
		if err != nil {
			return spec, err
		}
		if target != "" && !access.EventActionAllowed(ctx, h.db, u.ID, u.Permissions, strings.TrimPrefix(strings.TrimPrefix(p, "team_event_types."), "event_types."), target) {
			return spec, errors.New("event type not found or action not permitted")
		}
	}
	if p == "" || !u.Permissions.Has(p) || !access.Delegable(u.Permissions).Has(p) {
		return spec, fmt.Errorf("permission required: %s", p)
	}
	if op.Name == "import_event_type" && !u.Permissions.Has(strings.TrimSuffix(p, ".create")+".update") {
		return spec, errors.New("import requires event creation and configuration permissions")
	}
	return spec, nil
}
func (h *Handler) callAgentOperation(ctx context.Context, u AuthUser, op agentOperation) (any, int, error) {
	spec, err := h.checkAgentOperation(ctx, u, op)
	if err != nil {
		return nil, 403, err
	}
	r, err := h.operationRequest(ctx, op, spec)
	if err != nil {
		return nil, 400, err
	}
	w := httptest.NewRecorder()
	switch handlerName(spec.handler) {
	case "ListIdentityProviders", "createAgentIntegrationDraft", "agentApplicationLogs", "importAgentEventType":
		spec.handler(w, r.WithContext(context.WithValue(ctx, ctxKeyUser, u)))
	default:
		h.authorizedCall(w, r, u, spec.handler)
	}
	var result any
	if w.Body.Len() > 0 && json.Unmarshal(w.Body.Bytes(), &result) != nil {
		return nil, 500, errors.New("operation returned an unsupported response")
	}
	result = redactAgentData(result)
	if w.Code >= 400 {
		return result, w.Code, fmt.Errorf("%s failed (HTTP %d): %v", op.Name, w.Code, result)
	}
	return result, w.Code, nil
}
func agentFingerprint(v any) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

type agentToolInput struct {
	Target  string            `json:"target,omitempty"`
	ChildID string            `json:"child_id,omitempty"`
	Values  map[string]any    `json:"values,omitempty"`
	Query   map[string]string `json:"query,omitempty"`
}
type agentBatchInput struct {
	Operations []agentOperation `json:"operations" jsonschema:"1–100 named operations. Imported event types must have is_active=false and is_public=false. Existing bookings are not migrated."`
}

func (h *Handler) registerAgentTools(s *mcp.Server) {
	for name, spec := range h.agentOperations() {
		name, spec := name, spec
		mcp.AddTool(s, &mcp.Tool{Name: name, Description: spec.description, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: spec.method == "GET", DestructiveHint: agentBool(spec.method != "GET" && spec.method != "POST"), IdempotentHint: spec.method == "GET", OpenWorldHint: agentBool(spec.method != "GET")}}, func(ctx context.Context, _ *mcp.CallToolRequest, in agentToolInput) (*mcp.CallToolResult, map[string]any, error) {
			u, err := h.agentActor(ctx)
			if err != nil {
				return nil, nil, err
			}
			op := agentOperation{Name: name, Target: in.Target, ChildID: in.ChildID, Values: in.Values, Query: in.Query}
			if spec.method == "GET" {
				v, _, err := h.callAgentOperation(ctx, u, op)
				return nil, map[string]any{"result": v}, err
			}
			v, err := h.submitAgentChanges(ctx, u, []agentOperation{op})
			return nil, v, err
		})
	}
	mcp.AddTool(s, &mcp.Tool{Name: "submit_changes", Description: "Submit a reviewed batch of 1–100 configuration operations or imports. Sensitive operations return a Calnode review URL unless the user's review policy permits automatic execution. Approval applies to this exact immutable batch; no credentials or existing booking migration.", Annotations: &mcp.ToolAnnotations{DestructiveHint: agentBool(true)}}, func(ctx context.Context, _ *mcp.CallToolRequest, in agentBatchInput) (*mcp.CallToolResult, map[string]any, error) {
		u, err := h.agentActor(ctx)
		if err != nil {
			return nil, nil, err
		}
		v, err := h.submitAgentChanges(ctx, u, in.Operations)
		return nil, v, err
	})
	mcp.AddTool(s, &mcp.Tool{Name: "get_change_request_status", Description: "Read the status/results of your own connection's proposed batch. This cannot approve or execute it.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: agentBool(false)}}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		ID string `json:"id"`
	}) (*mcp.CallToolResult, map[string]any, error) {
		u, err := h.agentActor(ctx)
		if err != nil {
			return nil, nil, err
		}
		c, cred, _, err := h.loadAgentChange(ctx, in.ID, u.ID)
		if err != nil || cred != u.CredentialID {
			return nil, nil, errors.New("request not found")
		}
		return nil, map[string]any{"id": c.ID, "status": c.Status, "results": c.Results, "expires_at": c.Expires}, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "list_agent_operations", Description: "Discover supported management operations, their REST paths, target conventions and whether they need configuration review. Each operation still enforces current user and connection permissions.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, map[string]any, error) {
		u, err := h.agentActor(ctx)
		if err != nil {
			return nil, nil, err
		}
		items := []map[string]any{}
		for name, spec := range h.agentOperations() {
			p := operationPermission(spec)
			if u.Permissions.Has(p) || strings.HasPrefix(p, "event_types.") && u.Permissions.Has("team_"+p) {
				items = append(items, map[string]any{"name": name, "method": spec.method, "path": spec.path, "description": spec.description, "sensitive": spec.sensitive, "fields": agentFields(spec)})
			}
		}
		sort.Slice(items, func(i, j int) bool { return items[i]["name"].(string) < items[j]["name"].(string) })
		return nil, map[string]any{"operations": items}, nil
	})
}
func agentBool(b bool) *bool { return &b }

func (h *Handler) createAgentIntegrationDraft(w http.ResponseWriter, r *http.Request) {
	u, _ := userFromContext(r.Context())
	var values map[string]any
	if json.NewDecoder(r.Body).Decode(&values) != nil || rejectAgentSecrets(values) != nil {
		h.writeError(w, 400, "provider configuration without credentials is required")
		return
	}
	// Only fields from the provider form are accepted. A draft never changes live SSO.
	allowed := strings.Fields("name kind purpose client_id microsoft_tenant_id issuer_url pkce allow_members auto_provision approved_domains group_ids group_match groups_claim")
	for key := range values {
		found := false
		for _, k := range allowed {
			found = found || k == key
		}
		if !found {
			h.writeError(w, 400, "unsupported draft field: "+key)
			return
		}
	}
	if values["kind"] != "microsoft" && values["kind"] != "google" && values["kind"] != "oidc" {
		h.writeError(w, 400, "choose microsoft, google or oidc")
		return
	}
	values["enabled"] = false
	values["id"] = ""
	raw, _ := json.Marshal(values)
	id := uid.New()
	if _, err := h.db.ExecContext(r.Context(), `INSERT INTO mcp_integration_drafts(id,user_id,configuration_json,created_at)VALUES(?,?,?,?)`, id, u.ID, string(raw), time.Now().UTC().Format(time.RFC3339)); err != nil {
		h.writeError(w, 500, "could not save draft")
		return
	}
	h.writeJSON(w, 201, map[string]any{"id": id, "status": "draft", "complete_url": h.baseURL + "/admin/settings/identity?draft=" + id, "human_secret_required": true})
}
func (h *Handler) GetMCPIntegrationDraft(w http.ResponseWriter, r *http.Request) {
	u, _ := userFromContext(r.Context())
	if u.CredentialID != "" || !u.Permissions.Has("settings.security") {
		h.writeError(w, 403, "interactive administrator required")
		return
	}
	var raw string
	if h.db.QueryRowContext(r.Context(), `SELECT configuration_json FROM mcp_integration_drafts WHERE id=? AND user_id=?`, r.PathValue("id"), u.ID).Scan(&raw) != nil {
		h.writeError(w, 404, "draft not found")
		return
	}
	var v any
	_ = json.Unmarshal([]byte(raw), &v)
	h.writeJSON(w, 200, v)
}
func (h *Handler) agentApplicationLogs(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.QueryContext(r.Context(), `SELECT id,type,status,created_at FROM jobs ORDER BY created_at DESC LIMIT 100`)
	if err != nil {
		h.writeError(w, 500, "could not read application activity")
		return
	}
	defer rows.Close()
	items := []map[string]string{}
	for rows.Next() {
		var id, kind, status, at string
		if rows.Scan(&id, &kind, &status, &at) == nil {
			items = append(items, map[string]string{"id": id, "type": kind, "status": status, "created_at": at})
		}
	}
	h.writeJSON(w, 200, map[string]any{"items": items, "source": "application job activity; raw container logs are not exposed"})
}

// Filtering tools/list is an affordance; every execution still checks authority.
func (h *Handler) filterAgentTools(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		result, err := next(ctx, method, req)
		if err != nil || method != "tools/list" {
			return result, err
		}
		caller, ok := mcpCallerFromContext(ctx)
		if !ok || caller.Permissions == nil {
			return result, err
		}
		list, ok := result.(*mcp.ListToolsResult)
		if !ok {
			return result, err
		}
		copyResult := *list
		copyResult.Tools = []*mcp.Tool{}
		for _, tool := range list.Tools {
			if h.agentToolVisible(caller.Permissions, tool.Name) {
				copyResult.Tools = append(copyResult.Tools, tool)
			}
		}
		return &copyResult, nil
	}
}
func (h *Handler) agentToolVisible(g access.Grants, name string) bool {
	p := map[string]string{"list_event_types": "event_types.read", "get_event_type": "event_types.read", "get_available_slots": "availability.read", "get_booking": "bookings.read", "list_bookings": "bookings.read", "create_booking": "bookings.create", "reschedule_booking": "bookings.reschedule", "cancel_booking": "bookings.cancel", "get_meeting_notes": "notes.read", "get_transcript": "transcripts.read"}[name]
	if spec, ok := h.agentOperations()[name]; ok {
		p = operationPermission(spec)
	}
	if name == "list_agent_operations" || name == "get_change_request_status" {
		return g.Has("mcp.connect")
	}
	if name == "submit_changes" {
		for _, spec := range h.agentOperations() {
			if spec.method != "GET" && (g.Has(operationPermission(spec)) || g.Has("team_"+operationPermission(spec))) {
				return true
			}
		}
		return false
	}
	return g.Has(p) || strings.HasPrefix(p, "event_types.") && g.Has("team_"+p)
}
