package handler

// agentRequestFields lists the canonical REST request fields, checked against
// their declarations by TestAgentRequestFieldInventory. Unknown import fields
// fail explicitly instead of being silently discarded by encoding/json.
var agentRequestFields = map[string][]string{
	"CreateAvailabilityOverride": {"date", "end_date", "reason", "start_time", "end_time", "schedule_id", "is_available"},
	"CreateAvailabilityRule":     {"event_type_id", "schedule_id", "day_of_week", "start_time", "end_time"},
	"CreateAvailabilitySchedule": {"name", "timezone", "is_default"},
	"CreateDynamicLink":          {"host_ids", "duration_minutes", "organizer_id", "name", "duration_options", "location_type", "location_value", "destination_id"},
	"CreateEventType":            {"slug", "name", "display_order", "is_active", "is_public", "description", "duration_minutes", "slot_interval_minutes", "location_type", "location_value", "routing_mode", "buffer_before_minutes", "buffer_after_minutes", "min_notice_minutes", "max_future_days", "max_active_bookings", "max_per_day", "max_per_week", "allow_phone_call", "show_taken_slots", "invite_delivery", "team_id"},
	"CreateOOOEntry":             {"team_id", "dates", "reason", "public_note", "redirect_user_id"},
	"CreateQuestion":             {"label", "type", "options", "required", "position", "identifier", "hidden", "lock_if_prefilled"},
	"CreateSingleUseLink":        {"expires_in_hours", "expires_in_seconds", "custom_slots", "prefill"},
	"CreateTeam":                 {"name", "slug"},
	"PatchAvailabilitySchedule":  {"name", "timezone", "is_default"},
	"PatchBranding":              {"business_name", "logo_height", "logo_opacity", "banner_opacity", "privacy_url", "terms_url", "fallback_locale"},
	"PatchEmailSettings":         {"smtp_host", "smtp_port", "smtp_user", "smtp_pass", "smtp_tls", "smtp_starttls", "email_from", "email_from_name", "resend_api_key", "rsvp_address", "resend_webhook_secret"},
	"PatchEventType":             {"slug", "name", "display_order", "description", "duration_minutes", "slot_interval_minutes", "location_type", "location_value", "routing_mode", "rr_strategy", "buffer_before_minutes", "buffer_after_minutes", "min_notice_minutes", "max_future_days", "max_active_bookings", "max_per_day", "max_per_week", "max_per_month", "max_per_year", "max_duration_per_day", "max_duration_per_week", "show_first_slot_only", "future_limit_mode", "future_range_start", "future_range_end", "calendar_event_title", "redirect_url", "reply_to", "disable_cancel", "disable_reschedule", "require_cancel_reason", "allow_reschedule_past", "hide_notes_in_calendar", "hide_calendar_event_details", "requires_confirmation", "confirm_mode", "confirm_notice_minutes", "unconfirmed_blocks_slots", "require_email_verification", "allow_multiple_durations", "duration_options", "location_options", "destination_calendar_id", "schedule_id", "optimized_slots", "booking_form_config", "team_id", "is_active", "is_public", "allow_phone_call", "show_taken_slots", "invite_delivery", "archived", "msg_confirmation", "msg_cancellation", "msg_reschedule", "msg_reminder", "msg_greeting", "subj_confirmation", "subj_cancellation", "subj_reschedule", "subj_reminder", "html_confirmation", "html_cancellation", "html_reschedule", "html_reminder", "price_cents", "currency", "reminders"},
	"PatchLLMSettings":           {"enabled", "endpoint", "model", "api_key", "extra_instructions"},
	"PatchLiveKitSettings":       {"url", "api_key", "api_secret"},
	"PatchMe":                    {"name", "handle", "booking_accent", "timezone", "scheduled_timezone", "timezone_effective_at", "time_format", "week_start", "date_format", "notify_confirmation", "notify_cancellation", "notify_reschedule", "notify_reminder", "notify_host_booking", "notify_host_cancel", "notify_host_reschedule"},
	"PatchNotetakerSettings":     {"enabled", "stt_api_key"},
	"PatchStorageSettings":       {"recordings_enabled"},
	"PatchStripeSettings":        {"secret_key", "publishable_key", "webhook_secret", "clear"},
	"PatchTeam":                  {"name", "slug", "description", "logo_url", "location", "brand_color", "show_members_publicly"},
	"PatchTrackingSettings":      {"head_html", "csp_allow", "datalayer_enabled", "datalayer_fields", "gtm_container_id", "ga4_measurement_id"},
	"PatchZoomSettings":          {"client_id", "client_secret"},
	"PutMeetingDefaults":         {"conferencing_type"},
	"SetCalendarDestination":     {"provider", "account_email"},
	"SetEventTypeHosts":          {"hosts"},
	"UpdateAvailabilityOverride": {"reason", "start_time", "end_time", "is_available"},
	"UpdateAvailabilityRule":     {"day_of_week", "start_time", "end_time"},
	"UpdateOOOEntry":             {"dates", "reason", "public_note", "redirect_user_id"},
	"UpdateQuestion":             {"label", "type", "options", "required", "position", "identifier", "hidden", "lock_if_prefilled"},
}
