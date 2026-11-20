# MCP agents and review policy

Connect a remote MCP client to `BASE_URL/mcp` using OAuth, or a separately scoped
API key. Existing connections keep their saved permission ceiling: reconnect to
grant newly added configuration or team permissions. A role promotion alone does
not expand an existing connection. Current user permissions, current credential
permissions and resource membership are checked again at execution.

The consent screen offers four starting profiles. These are selections for that
connection, not new roles. Unavailable permissions are omitted, and the user can
adjust the remaining selection:

| Profile | Intended access |
| --- | --- |
| Read-only assistant | Event types, availability and authorized bookings |
| Scheduling assistant | Read-only plus booking creation/rescheduling/cancellation, one-time links, custom times and dynamic links |
| Event manager | Scheduling plus reusable event configuration, availability and personal meeting preferences |
| Workspace operator | All supported, delegable operations the user actually holds, including redacted audit and application job activity |

Transcript and meeting-note access is separately opt-in. Passwords, MFA recovery,
ownership, impersonation and credential creation are not agent operations.
Tool annotations describe read-only/destructive behavior to clients; annotations
are hints, not authorization or proof of human approval.

## Team boundary

Personal event types and team event types have separate permission groups.
Default Members can read event types for teams they belong to, generate one-time
links and offer custom times. They do not have to be assigned hosts, and they
cannot configure reusable team event types. Link generation and custom times are
independent grants. Dynamic links retain their separate instance/team scope.

Current team heads derive the corresponding team configuration grants from their
personal event permissions unless their role already supplies team grants. Every
team edit also checks that they are still a head of that particular team, or an
instance administrator. Creating the original event does not preserve team-editing
authority after stepping down. The same checks protect REST and MCP.

## Routine actions and sensitive configuration

Routine scheduling executes without an additional Calnode approval screen. The
MCP client decides how it obtains approval for booking, rescheduling, cancellation
and one-time links. These actions can notify attendees or disclose a booking URL;
grant only the permissions appropriate for the client.

Everyone can choose **Settings → Account security → Agent review preferences**
(or the same controls in **Connected apps**). It defaults on. Disabling requires interactive identity
verification. **Settings → Security & sign-in** can enforce review instance-wide;
personal choices cannot weaken that policy. Agents cannot change either policy.
This gate governs MCP configuration tools; it is not an approval layer for every
independent REST API integration.

Sensitive tools (reusable event changes, availability/preferences and administration)
either execute under that policy or return a review URL. `submit_changes` groups
up to 100 named operations, maximum 1 MiB. The human sees the exact immutable
proposal and current configuration, then approves or rejects it in their own
browser session. The agent cannot approve the proposal itself. Proposals expire
after 30 minutes; changed resources, revoked credentials, reduced permissions and
replay attempts are rejected.

Execution is sequential, not an all-or-nothing transaction. Results are recorded
per operation, and execution stops at the first error. A partial result must be
inspected before preparing another proposal. A process crash leaves `executing`
for operator reconciliation rather than silently replaying side effects.

## Import and configuration tools

Use `list_agent_operations` to discover names, accepted JSON fields, target
conventions and sensitivity. Named tools reuse the existing authenticated REST
handlers and validators; arbitrary URLs, SQL and unsupported import fields are
rejected. Exported descriptions, HTML and third-party content are untrusted data,
not instructions to authorize additional changes.

`import_event_type` combines the existing create and update handlers so advanced
event settings can be proposed in the same batch. Imports and `create_event_type`
always produce inactive/private events. Publishing is a separate explicit update.
An advanced-validation failure can leave an inactive draft; the error identifies
it so the client can correct that draft without repeating creation. Host assignments
and questions use their own tools after the event exists. Existing bookings and
calendar events are never imported or adopted.

Agents can prepare a disabled identity-provider draft without credentials. Its
completion URL opens the existing provider form; the human enters the secret and
decides whether to activate it. Secret-bearing settings fields are rejected, and
responses are redacted. External OAuth consent and account connections remain
interactive; an agent can select an already-connected booking account and change
non-secret preferences.

`get_application_logs` exposes bounded delivery/job status, not raw container
stdout or provider error payloads. `get_audit_logs` exposes security metadata with
legacy free-form details redacted. Use deployment observability for full logs.

The new management tools require an authenticated remote MCP connection. The
existing trusted local stdio scheduling tools keep their previous behavior.

## Verification

`go test ./internal/handler ./internal/server ./internal/booking` covers live
permission ceilings, team membership/head changes, separate link/custom-time
grants, immutable review, expiry/replay/revocation, partial failure, redaction and
advanced imports. An AST contract test detects drift in accepted REST fields.

`pnpm --dir frontend run test:visual` includes light/dark review UI checks.
`frontend/scripts/verify-agent-operations.mjs` additionally performs a real OAuth
PKCE flow, changes consent profiles, calls MCP, approves a proposal through the
SPA and verifies that the imported event remains inactive/private. It refuses
non-localhost or already-claimed instances. CI runs it against a fresh in-memory
server. Set `CALNODE_AGENT_TEST_URL` and optionally `CALNODE_SCREENSHOT_DIR` to run
it manually after building the frontend and server.
