package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"reflect"
	"runtime"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/access"
	"github.com/calnode/calnode/internal/uid"
)

// Every authenticated application handler is explicitly classified. Unknown
// handlers fail closed; the server contract test keeps the inventory complete.
var handlerPermissions = func() map[string]string {
	groups := map[string]string{
		"profile.read":          "GetMe ListDirectoryUsers",
		"profile.update":        "PatchMe UploadAvatar DeleteAvatar ChangePassword RevokeAllSessions StopImpersonation",
		"members.password":      "AdminSetPassword",
		"members.invite_member": "CreateInvite ListInvites RevokeInvite ResendInvite",
		"members.read":          "ListUsers ListUserUpcomingBookings",
		"members.archive":       "DeleteUser ArchiveUser RestoreUser",
		"members.update_role":   "SetUserRole SetUserCustomRole",
		"roles.manage":          "CreateRole UpdateRole DeleteRole",
		"roles.read":            "ListRoles",
		"impersonation.use":     "StartImpersonation",
		"impersonation.audit":   "ListImpersonationLogs ListSecurityAudit",
		"settings.security":     "TransferOwnership ClaimOwnership Reauthenticate GetSigninPolicy PutSigninPolicy",
		"teams.read":            "ListTeams GetTeam",
		"teams.create":          "CreateTeam",
		"teams.update":          "PatchTeam",
		"teams.delete":          "DeleteTeam",
		"teams.members":         "AddTeamMember UpdateTeamMember RemoveTeamMember",
		"settings.read":         "GetEmailSettings GetGoogleSettings GetMicrosoftSettings GetZoomSettings GetLiveKitSettings GetStorageSettings GetNotetakerSettings GetStripeSettings GetTrackingSettings GetLLMSettings",
		"settings.update":       "PatchEmailSettings TestEmailConnection SetupRSVPWebhook PatchGoogleSettings PutMicrosoftSettings PatchZoomSettings PatchLiveKitSettings PatchStorageSettings PatchNotetakerSettings PatchStripeSettings PatchTrackingSettings PatchLLMSettings TestLLMSettings",
		"branding.read":         "GetBranding",
		"branding.update":       "PatchBranding UploadBrandingLogo DeleteBrandingLogo UploadBrandingBanner DeleteBrandingBanner",
		"event_types.read":      "ListEventTypes GetEventType ListEventTypeHosts ListQuestionsAdmin ListEmailTemplates ListSingleUseLinks PreviewBookingPage PreviewBookingSlots PreviewEmailTemplate",
		"event_types.create":    "CreateEventType DuplicateEventType",
		"event_types.update":    "PatchEventType TransferEventType SetEventTypeHosts SendTestEmail CreateEmailTemplate DeleteEmailTemplate CreateQuestion UpdateQuestion DeleteQuestion",
		"event_types.delete":    "DeleteEventType",
		"event_types.links":     "CreateSingleUseLink RevokeSingleUseLink",
		"availability.read":     "ListAvailabilityRules ListAvailabilityOverrides ListAvailabilitySchedules ListOOOEntries",
		"availability.update":   "CreateAvailabilityRule UpdateAvailabilityRule DeleteAvailabilityRule CreateAvailabilityOverride UpdateAvailabilityOverride DeleteAvailabilityOverride DeleteAvailabilityOverrideGroup CreateAvailabilitySchedule PatchAvailabilitySchedule DeleteAvailabilitySchedule CreateOOOEntry UpdateOOOEntry DeleteOOOEntry",
		"bookings.read":         "ListBookings",
		"bookings.create":       "CreateBooking",
		"bookings.cancel":       "CancelBooking",
		"bookings.approve":      "ConfirmBooking DeclineBooking",
		"bookings.reschedule":   "RescheduleBooking",
		"bookings.reassign":     "ReassignBooking",
		"bookings.answers":      "GetBookingAnswers",
		"recordings.read":       "ListRecordings ListRecordingConsent DownloadRecording",
		"recordings.delete":     "DeleteAllRecordings DeleteRecording",
		"notes.read":            "GetBookingNotes",
		"notes.update":          "RegenerateBookingNotes",
		"transcripts.read":      "GetBookingTranscript",
		"webhooks.read":         "ListWebhooks",
		"webhooks.create":       "CreateWebhook",
		"webhooks.update":       "PatchWebhook",
		"webhooks.delete":       "DeleteWebhook",
		"webhooks.deliveries":   "ListWebhookDeliveries",
		"webhooks.rotate":       "RotateWebhookSecret",
		"integrations.read":     "CalendarStatus ListConnectedCalendars GetConnectionCalendars ZoomStatus",
		"integrations.update":   "ConnectCalendar ConnectCalDAV SetCalendarDestination PutConnectionCalendars DisconnectCalendarConnection DisconnectCalendar ConnectZoom DisconnectZoom",
		"api_keys.read":         "ListAPIKeys GetPermissionCatalog",
		"api_keys.create":       "CreateAPIKey UpdateAPIKey",
		"api_keys.revoke":       "DeleteAPIKey",
		"mcp.manage":            "ListOAuthConnections RevokeOAuthConnection",
	}
	out := map[string]string{}
	for p, names := range groups {
		for _, n := range strings.Fields(names) {
			out[n] = p
		}
	}
	return out
}()

func handlerName(next http.HandlerFunc) string {
	n := runtime.FuncForPC(reflect.ValueOf(next).Pointer()).Name()
	n = strings.TrimSuffix(n, "-fm")
	return n[strings.LastIndex(n, ".")+1:]
}

func (h *Handler) authorizedCall(w http.ResponseWriter, r *http.Request, u AuthUser, next http.HandlerFunc) {
	name := handlerName(next)
	if u.CredentialID != "" && (name == "ChangePassword" || name == "RevokeAllSessions") {
		h.writeError(w, 403, "interactive session required")
		return
	}
	p, known := handlerPermissions[name]
	if !known { // Anonymous wrappers are used only by middleware tests, never routes.
		if !strings.Contains(runtime.FuncForPC(reflect.ValueOf(next).Pointer()).Name(), ".func") {
			h.writeError(w, 403, "unclassified operation")
			return
		}
	} else if name != "Reauthenticate" && name != "ClaimOwnership" && name != "GetPermissionCatalog" {
		if !u.Permissions.Has(p) {
			h.logger.WarnContext(r.Context(), "permission denied", "actor_user_id", u.ID, "credential_id", u.CredentialID, "permission", p, "operation", name)
			h.writeError(w, 403, "permission required: "+p)
			return
		}
		if u.CredentialID != "" && !access.Delegable(access.Full()).Has(p) {
			h.writeError(w, 403, "this operation requires an interactive session")
			return
		}
		scope := u.Permissions[p]
		id := r.PathValue("id")
		slug := r.PathValue("slug")
		if strings.HasPrefix(p, "event_types.") {
			target := slug
			if target == "" && (name == "CreateSingleUseLink" || name == "ListSingleUseLinks") {
				target = id
			}
			if name == "RevokeSingleUseLink" {
				_ = h.db.QueryRowContext(r.Context(), `SELECT event_type_id FROM single_use_tokens WHERE token=?`, r.PathValue("token")).Scan(&target)
			}
			allowed := true
			if target != "" {
				if p == "event_types.read" {
					allowed = access.EventAllowed(r.Context(), h.db, u.ID, scope, target)
				} else {
					allowed = access.EventManageAllowed(r.Context(), h.db, u.ID, scope, target)
				}
			}
			if !allowed {
				h.writeError(w, 404, "event type not found")
				return
			}
		}
		if strings.HasPrefix(p, "bookings.") || strings.HasPrefix(p, "notes.") || strings.HasPrefix(p, "transcripts.") {
			if id != "" && !access.BookingAllowed(r.Context(), h.db, u.ID, scope, id) {
				h.writeError(w, 404, "booking not found")
				return
			}
		}
		if strings.HasPrefix(p, "teams.") && id != "" && scope != "all" {
			var n int
			_ = h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM team_members WHERE team_id=? AND user_id=?`, id, u.ID).Scan(&n)
			if n == 0 {
				h.writeError(w, 404, "team not found")
				return
			}
		}
		if strings.HasPrefix(p, "recordings.") && id != "" {
			var bookingID string
			if h.db.QueryRowContext(r.Context(), `SELECT COALESCE(booking_id,'') FROM recordings WHERE id=?`, id).Scan(&bookingID) == nil && scope != "all" && (bookingID == "" || !access.BookingAllowed(r.Context(), h.db, u.ID, scope, bookingID)) {
				h.writeError(w, 404, "recording not found")
				return
			}
		}
		// Legacy data handlers use IsAdmin for workspace visibility. Narrow credentials
		// cannot inherit their owner's global bypass; resource guards above remain in force.
		u.InstanceAdmin = u.IsAdmin || u.IsOwner
		if p != "profile.read" && p != "profile.update" {
			u.IsAdmin = scope == "all"
		}
		if (strings.HasPrefix(p, "teams.") && scope == "team" && id != "") || strings.HasPrefix(p, "recordings.") || (strings.HasPrefix(p, "event_types.") && scope == "team" && (slug != "" || name == "CreateSingleUseLink")) {
			u.IsAdmin = true
		}
	}
	u.Operation = p
	next(w, r.WithContext(context.WithValue(r.Context(), ctxKeyUser, u)))
}

func (h *Handler) loadAuthorization(ctx context.Context, u *AuthUser, keyID string) error {
	grants, err := access.Load(ctx, h.db, u.ID)
	if err != nil {
		return err
	}
	u.InstanceOwner = u.IsOwner
	u.Permissions = grants
	u.InstanceAdmin = u.IsAdmin || u.IsOwner
	if keyID != "" {
		var raw, expires sql.NullString
		if err = h.db.QueryRowContext(ctx, `SELECT permissions_json,expires_at FROM api_keys WHERE id=? AND user_id=?`, keyID, u.ID).Scan(&raw, &expires); err != nil {
			return err
		}
		if expires.Valid {
			exp, e := time.Parse(time.RFC3339, expires.String)
			if e != nil || !time.Now().Before(exp) {
				return sql.ErrNoRows
			}
		}
		// NULL is reserved for operator-created/bootstrap legacy keys; freeze on first use.
		if !raw.Valid {
			raw.String = grants.JSON()
			_, err = h.db.ExecContext(ctx, `UPDATE api_keys SET permissions_json=?,is_legacy=1 WHERE id=? AND permissions_json IS NULL`, raw.String, keyID)
			if err != nil {
				return err
			}
			_ = h.db.QueryRowContext(ctx, `SELECT permissions_json FROM api_keys WHERE id=?`, keyID).Scan(&raw.String)
		}
		u.Permissions = access.Intersect(grants, access.Parse(raw.String))
		u.CredentialID = keyID
		u.IsOwner = false // ownership is a human/session capability, never delegated
	}
	u.CanImpersonate = u.Permissions.Has("impersonation.use")
	u.CanManageUsers = u.Permissions.Has("members.read")
	u.CanManageSettings = u.Permissions.Has("settings.update")
	u.CanManageTeams = u.Permissions.Has("teams.update")
	return nil
}

func (h *Handler) canAccessUser(ctx context.Context, u AuthUser, p, target string) bool {
	if u.CredentialID != "" && !access.Delegable(access.Full()).Has(p) {
		return false
	}
	if target == u.ID {
		return true
	}
	scope := u.Permissions[p]
	return scope == "all" || (scope == "team" && access.Related(ctx, h.db, u.ID, target))
}

func (h *Handler) securityAudit(ctx context.Context, actor, action, target string, details any) {
	b, _ := json.Marshal(details)
	if _, err := h.db.ExecContext(ctx, `INSERT INTO security_audit_logs(id,actor_user_id,action,target_id,details) VALUES(?,?,?,?,?)`, uid.New(), actor, action, target, string(b)); err != nil {
		h.logger.ErrorContext(ctx, "security audit failed", "action", action, "error", err)
	}
}
func (h *Handler) GetPermissionCatalog(w http.ResponseWriter, r *http.Request) {
	u, _ := userFromContext(r.Context())
	h.writeJSON(w, 200, map[string]any{"items": access.Catalog, "presets": map[string]access.Grants{"admin": access.Full(), "member": access.Member(false)}, "permissions": u.Permissions, "delegable": access.Delegable(u.Permissions)})
}
func (h *Handler) ListSecurityAudit(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.QueryContext(r.Context(), `SELECT actor_user_id,action,target_id,details,created_at FROM security_audit_logs ORDER BY created_at DESC LIMIT 200`)
	if err != nil {
		h.writeError(w, 500, "could not load audit")
		return
	}
	defer rows.Close()
	items := []map[string]string{}
	for rows.Next() {
		var a, b, c, d, e string
		if rows.Scan(&a, &b, &c, &d, &e) == nil {
			items = append(items, map[string]string{"actor_user_id": a, "action": b, "target_id": c, "details": d, "created_at": e})
		}
	}
	h.writeJSON(w, 200, map[string]any{"items": items})
}
