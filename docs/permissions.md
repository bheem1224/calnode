# Permissions and delegated access

The server owns the permission catalog in `internal/access/access.go`. Every authenticated handler has an explicit action mapping. Unknown handlers fail closed; the route-inventory test checks for missing mappings. Public booking and token routes retain their separate booking authorization checks.

## People and ownership

- One Owner and any number of Admins share administrative actions. Admins cannot change, archive, remove, reset credentials for, or impersonate the Owner.
- Only the Owner can transfer ownership to an active Admin. The previous Owner remains an Admin. A transaction and unique database index enforce the single-owner invariant.
- Migration 93 enables a one-time ownership claim only when an older database has active Admins and no Owner. A recent interactive password verification or recent identity-provider sign-in is required. Claims and transfers are audited and notify active administrators when email is configured. Recent SSO sign-in is not an MFA or identity-provider step-up guarantee.
- Custom roles select actions and resource boundaries. Unselected actions are denied, including for Team Heads; users retain their own profile access. Personal event types can be managed by their owner or an explicitly authorized instance-wide operator. Assignment as a host does not grant configuration access. Team event configuration additionally requires current Head authority for that team or actual instance Admin/Owner authority.
- Role assignment cannot target yourself or the Owner. Granting Admin additionally requires the explicit Invite Admins permission. Role deletion is refused while members are assigned, and unused invitations tied to a deleted role are revoked.
- Support impersonation rechecks the operator's active account, current permission, session lifetime/MFA policy and target protection on every request. Delegated support roles cannot impersonate Admins. Effective actions are bounded by both accounts' grants; support sessions cannot authorize MCP or change credentials, calendar connections, webhooks, or security settings.

## Invitations

Invitations bind the email and assigned Member/Admin/custom role on the server. They expire, are single use, and recheck the issuer's current authority when claimed. Clients cannot change the assigned role during signup. Admin invitations require both invitation access and the explicit Invite Admins permission; this can be delegated to an automation key.

## API keys, MCP, and webhooks

A credential's effective access is the intersection of its saved grants and its owner's current permissions. Promotion never silently expands a saved credential. Removing a permission or archiving its owner restricts access on the next request.

New API keys require explicit permissions and an expiry (90 days by default, up to one year). Only the creation response contains the secret. Ordinary API keys cannot create credentials, edit role definitions, impersonate users, or perform ownership and personal account-security operations. Review updates a key's permissions and expiry without revealing its secret. Authorized administrators can inspect and revoke other accounts' keys and MCP connections.

MCP connections require the Connect an agent permission and OAuth consent for individual tool actions. Read operations are selected initially; mutation tools require explicit consent. Access tokens last one hour, are bound to this server's MCP resource, and use single-use authorization codes and rotating refresh tokens. User permission changes also constrain refresh and tool execution. The local stdio transport remains an operator trust boundary.

Webhook creation, editing, deletion, delivery inspection, and secret rotation have separate actions. A subscription stores its creator's effective grant snapshot. Authorization is checked before enqueue and again before each outbound delivery/retry, including the booking/team boundary and the data permission (recording, transcript, notes, or attendee answers). A queued job is not a permanent permission grant. Signature secrets can be rotated; receivers must be updated immediately.

## Existing deployments

Migration freezes existing key, OAuth, and webhook grants. Older keys and connections are marked for review. Legacy member access is preserved to avoid silently breaking existing integrations; Members shows a Legacy access badge and an Apply Member permissions action. Assign an explicit custom role or apply the default Member policy to remove that legacy access. New Members cannot create API keys or webhooks unless their assigned role allows it.

Review existing keys to set narrower grants and expiration. Reconnect legacy MCP apps to obtain explicit OAuth consent grants. Audit records are available in Settings → Members & Roles to roles with View security audit permission. Ownership recovery does not reopen automatically after it has been used.

## Shared scopes and profiles

- **Personal event types** uses `event_types.*` with Own resources or Entire instance. Old saved `team` scopes are read as `own`; they never turn into team-event grants. New ambiguous personal-event `team` grants are rejected.
- **Team event types** uses `team_event_types.*` with Assigned teams or Entire instance. Reading, generating links and offering custom times are independent of configuration. Membership alone does not allow creation, editing or deletion of reusable team events.
- **Bookings** uses actual booking participation for Own resources; Assigned teams additionally includes bookings of the caller's current teams. Owning or being assigned to a reusable event type does not grant access to every booking made through it. Lists and record-level operations enforce this same boundary.
- Built-in Members who are current Team Heads receive default team configuration capabilities. Custom roles are explicit and do not inherit missing team actions. Review the team-event actions on custom roles assigned to Heads when upgrading.
- The server defines the shared read-only, scheduling, event-management and workspace-operation profiles in `internal/access/profiles.go`. Role/key editors and MCP consent use these same operation bundles, clipped to the grantor. MCP transport permissions and non-delegable interactive operations remain separate safeguards.
- Security/application log access uses `logs.read` across the API, UI and MCP. `impersonation.audit` covers the separate impersonation-history endpoint; it does not grant application/security-log access.
- The REST event list, individual-record checks and both MCP event-list tools honor personal and team grants independently. `scope=all` requests every authorized record, without granting additional access. Team-only roles can use the Event Types navigation and list.

The cross-channel regression matrix is in `internal/handler/rbac_parity_internal_test.go`. See [feature verification](feature-verification.md) for the broader automated coverage and isolated browser runner.

## Teams and one-time scheduling

Only actual instance Admins/Owner can add/remove team members or appoint Heads. A team may have multiple Heads. Heads manage their team's profile, reusable team event types, and team-wide out-of-office entries; they do not gain authority over other teams or personal schedules. A team-wide block applies to the team's members, not to an outside administrator who created it. Members cannot publish reusable event types using unrelated people's calendars.

**Dynamic links → Create** is an independent action with own/team/instance boundaries. The built-in Member default is instance-wide so an associate or secretary can coordinate other people's availability. A group requires at least two distinct active people; an own-only grant cannot authorize another person's participation. Team scope requires each selected person to share a team with the creator.

Creating a dynamic link now mints a server-side, expiring single-use `/t/{token}` link, audited with its creator and host set. It uses the normal collective booking pipeline: availability, live calendar conflicts, transaction-time booking conflicts, confirmation delivery and atomic token consumption. Creator permissions and active hosts are rechecked on use. Expiry defaults to seven days and is configurable. Old unsigned `/u/alice+bob` and public group booking endpoints return 410; recreate those links from the builder.

For MFA enrollment, delegated Member recovery, protected Admin/Owner recovery and the emergency server command, see [security and sign-in](security-signin.md).

These controls support least privilege and request-time authorization. They do not constitute an independent security audit or OWASP certification.
