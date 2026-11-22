# Branch feature verification

This inventory groups the branch's additions by user-visible behavior. Existing
regression suites are reused; additional tests cover permission drift and compact
security controls. Passing these tests is not a security certification or proof
that every possible provider/tenant configuration works.

| Feature area | Automated verification |
| --- | --- |
| Shared user/API-key/MCP scopes, personal vs team events, booking list/detail boundaries, explicit custom-role denials, Head demotion and live revocation | `internal/access/access_test.go`, `internal/handler/rbac_parity_internal_test.go`, `permission_grants_test.go`, `authorization_contract_test.go` |
| Role/key profiles, team-only navigation, scoped key creation and stored grants | `frontend/src/security-permissions.visual.test.ts`, `frontend/scripts/verify-permission-settings.mjs` (called by agent verification) |
| Owner claim/transfer, role assignment, invitations, protected account recovery and impersonation | `roles_test.go`, `invites_test.go`, `auth_security_internal_test.go`, `security_regression_internal_test.go` |
| Password policy, passkeys, MFA, recovery codes and personal/company agent review policy | `meeting_policy_test.go`, `auth_security_internal_test.go`, `mcp_operations_internal_test.go`, `frontend/scripts/verify-native-auth.mjs` |
| Entra/Google/OIDC setup, group/domain checks, account linking, PKCE and calendar-only isolation | `identity_providers_test.go`, `signin_policy_test.go`, `internal/identityprovider`, `frontend/scripts/verify-identity-providers.mjs` |
| OAuth MCP grants, profiles, reviewed changes/imports, replay/expiry/revocation and redaction | `mcp_oauth_test.go`, `mcp_operations_internal_test.go`, `frontend/scripts/verify-agent-operations.mjs` |
| MCP grants/refresh after closing and reopening SQLite; client continuity after transport recreation | `internal/handler/mcp_oauth_restart_internal_test.go`, `internal/server/mcp_discovery_test.go` |
| Second-resolution one-time-link expiry, conflicting units, duration overflow, and expired public-link rejection | `internal/handler/single_use_seconds_test.go` |
| Private one-time booking reschedule availability, fixed booked duration/hosts, conflict/cap exclusions and manage-token boundaries | `internal/handler/manage_slots_test.go`, `frontend/scripts/verify-booking-flows.mjs` |
| Multiple durations/locations, public/embedded/manage form behavior and calendar actions | `durations_locations_test.go`, `location_choices_test.go`, `booking_parity_test.go`, `booking_surfaces_contract_test.go`, `frontend/scripts/verify-booking-flows.mjs` |
| Real draft preview with scheduling settings and disabled booking writes | `booking_preview_test.go`, `frontend/scripts/verify-booking-flows.mjs` |
| One-time/custom-time links, exact candidate slots, live busy conflicts and atomic token consumption | `single_use_test.go`, `internal/booking`, `internal/worker/notification_lifecycle_test.go` |
| Dynamic-link organizers, advanced duration/location/calendar overrides and personal defaults | `meeting_policy_test.go`, `security_regression_internal_test.go`, `frontend/scripts/verify-native-auth.mjs` |
| Weekly schedules, timezone handling, team OOO, notice/buffers/limits and booking concurrency | `internal/slots`, `internal/booking`, `team_scoping_test.go`, `booking_concurrency_test.go`, `limits_and_future_test.go` |
| Form questions, phone countries, additional guests, validation and injection rejection | `book_questions_test.go`, `question_types_prefill_test.go`, `booking_phone_test.go`, `booking_abuse_test.go`, booking browser matrix |
| Saved email templates, sanitized HTML preview/defaults and outgoing cancellation/reschedule content | `email_templates_test.go`, `notification_library_internal_test.go`, `frontend/src/notification-settings.visual.test.ts`, `frontend/scripts/verify-notification-settings.mjs` |
| Attendee/host invitations, reminders/cancellation/rescheduling, provider contracts and delivery recovery | `booking_email_test.go`, `internal/mailer`, `internal/worker`, `internal/gcal`, `internal/calendar/microsoft`, SMTP-capture booking browser matrix |
| Multi-scope webhooks, payload privacy, signatures, retry/backoff, expiry/booked lifecycle and permission changes before retry | `internal/webhook`, `internal/worker/notification_lifecycle_test.go`, `notification_library_internal_test.go`, notification browser verification |
| Calendar destinations, refresh/revocation failures, reconnect and fail-closed availability | `internal/connstore`, `internal/oauthstore`, `internal/gcal`, `internal/calendar/microsoft`, `calendar_reconcile_internal_test.go`, `booking_calendar_error_test.go` |
| Video room authority, recording/egress, payment and transcription integrations | `internal/livekit`, `livekit_reschedule_internal_test.go`, `internal/stripe`, `internal/stt` |
| Compact security screens, keyboard help, policy submission, enforced review state and light/dark styles | `frontend/src/security-permissions.visual.test.ts`, `auth-security.visual.test.ts`, permission-settings browser verification |
| Mobile team search/addition, long member names/emails, reachable member/invitation actions, touch spacing and ordinary-member configuration denials | `frontend/src/teams-mobile.visual.test.ts` (320px/390px, light/dark) |
| Duplication requires source read access; preview host substitution respects publishing scope; assigned-host read endpoints and delegated team-admin host configuration | `internal/handler/rbac_parity_internal_test.go` |

Handler test names without a directory above live in `internal/handler/`.

## Deployment review: 2026-11-21

The local browser review covered 26 main/settings routes at 320px, the team
search-and-add flow at 390px, populated member administration, mobile custom-time
dialogs, and mobile/desktop editor navigation. Regression tests cover both themes.
The automated feature suite also passed its 24 hosted/embed/manage booking flows.

Team routing priority is now effective: lower team-member numbers rank first;
event host order breaks ties. Personal event ordering is unchanged. The same
ordering helper feeds availability, actual assignment and unsaved previews.
Administrators can edit priority in the team member controls; this does not grant
ordinary members or credentials additional membership-management authority.

Webhook reliability regressions now cover explicit empty payload selections,
filtered single-use events, separate authorization metadata, atomic booking/outbox
rollback, failed fan-out recovery after confirmation completes, stable delivery
identity, exhausted-crash status, retry windows/Retry-After, and scoped manual
replay. See `internal/webhook/reliability_test.go`,
`internal/worker/reliability_outbox_test.go`, `internal/worker/retry_test.go`,
`internal/handler/webhook_retry_internal_test.go`,
`internal/handler/routing_priority_internal_test.go`,
`internal/handler/routing_priority_booking_test.go` and
`frontend/src/webhook-reliability.visual.test.ts`.

Local tests and dependency scans do not verify the deployed environment. Before
opening production traffic, verify the actual Entra tenant/group restrictions,
calendar refresh/revocation behavior, external invitation/reminder delivery,
trusted-proxy configuration, and a backup restore using the deployment's encryption
key. No production accounts, credentials, or infrastructure were changed by this
review.

## Run the checks

```sh
corepack pnpm@10.32.1 --dir frontend run check
corepack pnpm@10.32.1 --dir frontend run check:i18n
corepack pnpm@10.32.1 --dir frontend run test:visual
corepack pnpm@10.32.1 --dir frontend run build
node --test internal/handler/assets/booking-logic.test.js internal/handler/assets/booking-ui.test.js
go test ./...
go vet ./...
go build ./cmd/calnode
```

Build the frontend **before** compiling the application used for browser testing.
`frontend/scripts/verify-feature-suite.mjs` starts a separate in-memory instance for
native authentication, identity settings and agent/notification/permission flows,
then runs the booking matrix with its local SMTP capture. It launches from a new
temporary directory so a developer's `.env` is not loaded. No running instance or
real calendar/mail credentials are required. All child processes are stopped on
success or failure.

PowerShell example:

```powershell
go build -o "$env:TEMP/calnode-feature-test.exe" ./cmd/calnode
$env:CALNODE_TEST_BINARY = "$env:TEMP/calnode-feature-test.exe"
corepack pnpm@10.32.1 --dir frontend run test:flows
```

On POSIX, build a temporary executable and export its absolute path as
`CALNODE_TEST_BINARY` before running the same pnpm command. Set
`CALNODE_SCREENSHOT_DIR` to retain light/dark screenshots in a chosen directory.
`--identity-only` runs only the provider-settings flow; CI uses this alongside its
existing native-authentication, booking and agent verification steps.

Provider tests use HTTP contract fixtures, and mail tests use local capture.
Production readiness still requires a tenant-level smoke test of the actual SSO,
calendar, conferencing and mail credentials, plus a backup/restore exercise.
