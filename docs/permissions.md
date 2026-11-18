# Permissions and delegated access

The server owns the permission catalog in `internal/access/access.go`. Every authenticated handler has an explicit action mapping. Unknown handlers fail closed; the route-inventory test checks for missing mappings. Public booking and token routes retain their separate booking authorization checks.

## People and ownership

- One Owner and any number of Admins share administrative actions. Admins cannot change, archive, remove, reset credentials for, or impersonate the Owner.
- Only the Owner can transfer ownership to an active Admin. The previous Owner remains an Admin. A transaction and unique database index enforce the single-owner invariant.
- Migration 93 enables a one-time ownership claim only when an older database has active Admins and no Owner. A recent interactive password verification or recent identity-provider sign-in is required. Claims and transfers are audited and notify active administrators when email is configured. Recent SSO sign-in is not an MFA or identity-provider step-up guarantee.
- Custom roles select actions and resource boundaries. Unselected actions are denied; users retain their own profile access. Own event types can be managed by their owner or team head; an assigned host alone remains read-only. Explicit team grants cover the caller's teams. Global administrative actions and personal account settings expose only supported boundaries.
- Role assignment cannot target yourself or the Owner. Granting Admin additionally requires the explicit Invite Admins permission. Role deletion is refused while members are assigned, and unused invitations tied to a deleted role are revoked.

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

These controls support least privilege and request-time authorization. They do not constitute an independent security audit, MFA deployment, or OWASP certification.
