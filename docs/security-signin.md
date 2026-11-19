# Instance security and sign-in

Open **Settings → Identity & integrations** to add Microsoft Entra ID, Google, or generic OpenID Connect providers. **Security & sign-in** contains the shared policies (Member SSO, administrator password recovery, native MFA, session lifetime). Settings apply without restarting.

Each provider has a name, credentials and a purpose: **SSO only**, **SSO + calendar & conferencing**, or **Calendar & conferencing only**. Generic OIDC supports SSO only. Integrations-only apps cannot sign in or provision accounts. Google Meet / Microsoft Teams still depend on the vendor APIs and connected account capabilities; Zoom and CalDAV retain their separate settings.

For a Microsoft-managed company:

1. Add a Microsoft provider for SSO with your **Microsoft tenant ID** (the tenant object UUID), client ID and secret. Register the exact SSO redirect URL displayed after saving. The sign-in scopes are `openid profile email User.Read`.
2. Optionally add another Microsoft provider for personal calendars, purpose **Calendar & conferencing only**, tenant blank (`common`). Its Entra app registration must support work/school and personal accounts. Register `/v1/calendar/callback` on your instance. Calendar tokens remain bound to the app that issued them, including after refresh and restart.
3. Enable automatic enrollment on the SSO provider. Add approved domains and up to **20 security group object IDs**. Add values individually or paste comma-separated values, then press **Add** or Enter. The email domain AND the group rule must pass: **Any listed group** by default, **All listed groups** if selected. New accounts receive the built-in Member role. Transitive membership is checked at every sign-in for automatically enrolled accounts; failed/unavailable checks deny access. Turning automatic enrollment off stops new accounts while retaining checks for existing automatic accounts.
4. Existing users should link a new provider from **Settings → Account security** while they can still sign in. Then enable **Require SSO sign-in for Members** in Security & sign-in. Members cannot use passwords, emailed login links, resets or local passkeys under that policy. Owner/Admin native access has its own switch. Keep and test your administrative recovery method before enforcing access.
5. Changes require a verified interactive session (passkey/authenticator, password before MFA enrollment, or recent SSO when no local factor is enrolled). Configuration changes are audited, affected provider sessions are revoked, and stale callbacks are rejected. You cannot disable the provider you are currently using or remove the last required sign-in method. Changing an app's client ID/issuer/tenant with linked accounts requires creating a new provider and explicitly linking/reconnecting.

Microsoft Graph's `/me/checkMemberGroups` uses delegated `User.Read`; no application-wide directory permission is requested. See [Microsoft's permission table](https://learn.microsoft.com/en-us/graph/api/directoryobject-checkmembergroups?view=graph-rest-1.0).

## Generic OpenID Connect

Create a **confidential web client** using authorization code flow and the exact HTTPS issuer from its discovery document. Calnode requests `openid profile email`, verifies the signed ID token's issuer, audience, expiry and nonce, and enables **PKCE S256** by default. Advanced settings allow disabling PKCE for legacy confidential clients. Client secrets are required and encrypted at rest. Discovery, token and signing-key URLs must use HTTPS.

Automatic enrollment requires `email_verified: true` and an approved email domain. Optional group restrictions read a string array in the **signed ID token**, claim `groups` by default; configure the claim name if necessary. Calnode does not infer provider-specific directory memberships from arbitrary profile data or a separate userinfo response.

## Invitations, identity linking and migration

An invitation is email-bound, expires after seven days and is single-use. The provider must verify the invited address. The issuer's role-grant authority is rechecked when accepting. Invited accounts bypass automatic enrollment domains/groups on initial and later sign-ins, while still following the provider and instance policies. Invited Admins may choose a password when enabled.

New identities are bound to `(provider, issuer, subject)`. A new provider **never merges accounts solely by email**: an existing user must link it from their own recently verified session. Automatically enrolled accounts stay tied to their enrolling provider, so another provider cannot bypass the enrollment checks. The same provider cannot be linked to two accounts.

The migration imports existing effective Google/Microsoft configuration once, including the separate Microsoft personal-calendar app, existing grants and enrollment rules. Existing registered callback URLs remain valid; old settings URLs redirect to Identity & integrations and the legacy configuration write endpoints return 409. The old `microsoft_tenant_id` database column already had that name; provider records also use `microsoft_tenant_id`.

A frozen set of pre-migration accounts retains first-login matching through the original trusted Google/Microsoft app only, preserving historical admission. Existing Microsoft object-ID bindings take priority, including renamed UPNs. This exception never applies to new providers or accounts created after migration. Domain/group-enrolled accounts retain their provider binding. Review and test migration on a restored copy before rollout.

## Offboarding and recovery

Group removal is evaluated at the next sign-in, not continuously during an existing session. For immediate offboarding, archive the account or revoke sessions; archiving also blocks API keys and MCP credentials. Revoking sessions also revokes MCP access tokens. Owner/Admin local password access deliberately does not consult Entra groups. Archive a departed administrator rather than relying on group removal alone.

When Member SSO-only access is enabled, assigning a custom permission role does not make someone a password-capable Admin. Demotion immediately makes a password session ineligible. Existing password hashes are retained for rollback, but cannot authenticate while the policy denies that method.

An invite exception does not exempt an account from Microsoft's own tenant/conditional-access rules. Configure corporate MFA and other identity-provider requirements in Entra. Calnode does not bypass them.

## Native MFA and passkeys

Open **Settings → Account security** to register a passkey (recommended), an authenticator app using TOTP, and recovery codes. Passkeys require an HTTPS DNS hostname, except `http://localhost` during development; IP addresses such as `127.0.0.1` are not valid relying-party domains. Their relying-party ID and allowed origin come from `BASE_URL`, never the request's Host header. Device user verification (PIN/biometric) and discoverable credentials are required. Changing `BASE_URL` to another host can make existing passkeys unusable: arrange backup access before moving domains.

TOTP secrets are encrypted using the existing database encryption key. TOTP time steps cannot be reused, and recovery codes are random, hashed at rest, and single-use. Save recovery codes outside the scheduler; generating another set invalidates the previous set. Register a second passkey before relying on a single device. Removing the last authentication method is refused when MFA is required.

In **Settings → Security & sign-in**, enable MFA for local Owner/Admin sign-in and optionally for native Members. Enroll your own factor first. These switches apply to native authentication; configure SSO MFA/Conditional Access in Microsoft Entra separately. The app does not assert that a recent SSO login independently proves MFA. Disabling SSO-only Members enables native authentication for Members too; a custom role does not turn a Member into an instance Admin.

Passwords, emailed password resets, and emailed magic links cannot issue full access for an MFA-enrolled account without factor verification. A required-MFA account with no factor receives a short setup-only session. It cannot access application data, API keys, or MCP authorization until enrollment finishes. Existing sessions are checked against current policy on every authenticated request. Default maximum session age is 12 hours, configurable in Settings; changing the limit also constrains existing sessions.

## Helpdesk and Owner recovery

- A role with **Reset passwords** or **Recover MFA** may recover Members. Only actual instance Admins/Owner may recover another Admin, regardless of custom-role grants. The Owner cannot be recovered or impersonated by an Admin.
- MFA recovery requires the operator's own recent factor verification (or recent allowed SSO sign-in), and an identity-verification/support-ticket reference. A native operator must enroll their own MFA before authorizing MFA recovery. Verify the person through an established company channel; an email request by itself is insufficient.
- **Recover MFA** revokes the target's factors, recovery codes, browser sessions, pending login/recovery links and MCP access/authorization codes. Administrator recovery also revokes API keys. It issues a single-use link lasting 15 minutes. That link creates a 10-minute setup-only session; the person must enroll replacement authentication before app access. A password alone cannot bypass this recovery requirement.
- Optional MFA can be disabled by the same authorized recovery operation. When policy requires MFA, disabling it is refused; use re-enrollment. Password resets preserve MFA. Administrator password recovery also revokes Admin API keys. Password changes/resets revoke other browser sessions, pending authentication links and MCP connections.
- Recovery and factor changes are audited; affected users and active administrators receive queued security notices when email is configured. Check SMTP and the delivery health panel before depending on notices.

For the sole Owner, first use a backup passkey or recovery code. If all factors are lost, a trusted server operator can run:

```sh
calnode recover-owner --confirm
```

Run with the normal instance `DATABASE_URL` and `BASE_URL`, on the server or inside its container. The command revokes the Owner's factors, sessions, MCP credentials and API keys and prints a one-use recovery link; treat the output as a password. It does not change ownership or enable password authentication for other accounts. After replacement enrollment, it grants at most **one hour** of Owner access to repair normal sign-in settings. The normal login policy still applies afterward. Protect server/database access: anyone with that access already crosses the application's trust boundary.

## Before production rollout

1. Take and restore-test a database backup **with its encryption key**; MFA seeds and provider credentials depend on it. Restrict the key/DB volume to the service account (0600 on Unix; use NTFS ACLs on Windows). Test the migration and recovery command on a disposable restored copy first.
2. Enroll backup Owner/Admin factors, save recovery codes, then enable local Admin MFA. Test Microsoft group admission/removal and invitation exceptions with real tenant test accounts. Provider integration tests use controlled fakes; they do not validate your tenant configuration.
3. Check **Security & sign-in → Delivery & calendar health**, SMTP delivery, calendar reconnect, and failed-job retry. Booking confirmation work is committed durably with free booking creation, approval, or successful payment. Delivery retries are at least once: a partial failure/crash can resend a notification. A retry is not an exactly-once guarantee.
4. Exercise concurrent bookings, approvals, cancellations and rescheduling under your expected load on staging, and rehearse a restore/restart. Calendar checks fail closed at booking time when required hosts cannot be checked. An external calendar can still change after a check; this is not a distributed calendar lock.
5. Keep browser and Go dependency scans enabled in CI. These tests and controls support a security review; they are not an independent penetration test, OWASP certification, or a guarantee that every exploit has been eliminated.


## Public abuse controls

IP throttles are combined with durable sliding-window budgets in SQLite. Account,
recipient, event and instance counters apply independently, including across client
IP changes and process restarts. Counter keys are hashed and expired counters are
removed automatically. A denied reservation commits no partial budget charges.

Password attempts against the same normalized account incur increasing temporary
delays after five attempts, from one second up to five minutes. Backoff expires
after 15 minutes of inactivity and successful password verification clears it.
Unknown accounts follow the same path. OTP/recovery-code verification is additionally
limited to ten attempts per account per five minutes, across login challenges.
Passkey/SSO and password recovery remain
available. MFA enforcement remains an explicit company policy, off by default.

Booking verification allows one send per recipient per minute and five per hour,
across events. Each code accepts at most five guesses and succeeds once. A refused
resend preserves the outstanding code. Password-reset and magic-link requests share
a separate recipient cooldown of one minute and five sends per hour, applied to
both known and unknown accounts. The booking assistant directs users to the normal
form when email verification is required.

The booking service enforces hidden guest controls and the maximum of ten extra
guests for every caller, including single-use links. Booking budgets are reserved
inside the booking transaction: failed bookings spend none, cancellations do not
refund them, and rotating the organizer email cannot reset event, instance or guest
recipient limits. Calendar-provider invitations count toward these booking budgets;
they do not pass through Calnode's SMTP/Resend mail budget.

MCP registration grants no permissions. Consent records the chosen subset of the
approving user's permissions; each authenticated request intersects that snapshot
with the user's current permissions. Promotion cannot expand an existing grant,
and permission removal takes effect immediately. Registration accepts at most ten
callback URLs (2048 characters each), HTTPS, HTTP loopback, and hierarchical native
callbacks using reverse-domain schemes or the established claude/vscode/
vscode-insiders/cursor schemes. Credentials, fragments and executable/content URL
schemes are refused, including for previously registered callback values. Abandoned
registrations without tokens or live authorization codes expire after 24 hours;
clients with refresh grants are retained even when their access token has expired.

Advanced deployment overrides (positive integers; invalid/zero values retain the
default). Set these in the service environment and restart. Sliding-window budgets
apply per running instance database; concurrency bounds apply per process.

| Environment variable | Default |
| --- | ---: |
| `LOGIN_ATTEMPTS_PER_MINUTE` | 600 across accounts |
| `AUTH_EMAIL_HOURLY_LIMIT` | 300 reset/magic-link requests |
| `VERIFICATION_EVENT_HOURLY_LIMIT` | 120 sends per event |
| `VERIFICATION_HOURLY_LIMIT` | 600 sends across events |
| `BOOKING_HOURLY_LIMIT` | 1000 successful bookings |
| `BOOKING_EVENT_HOURLY_LIMIT` | 120 successful bookings per event |
| `BOOKING_RECIPIENT_HOURLY_LIMIT` | 30 bookings per organizer/guest address |
| `MCP_REGISTRATION_DAILY_LIMIT` | 100 registrations |
| `MCP_CLIENT_LIMIT` | 1000 stored clients |
| `AI_CONVERSATIONS_HOURLY_LIMIT` | 120 assistant requests |
| `AI_EVENT_HOURLY_LIMIT` | 60 assistant requests per event |
| `EMAIL_HOURLY_LIMIT` | 6000 SMTP/Resend recipients, including retry attempts |
| `EMAIL_RECIPIENT_HOURLY_LIMIT` | 100 sends per recipient, including retries |
| `CALENDAR_CHECK_CONCURRENCY` | 32 provider free/busy calls |
| `AI_REQUEST_CONCURRENCY` | 4 model calls, including streamed calls |
| `EMAIL_SEND_CONCURRENCY` | 8 SMTP/Resend sends |

Tune these to expected traffic and monitor throttling, delivery errors and calendar
health. Saturated external work fails promptly instead of creating an unbounded
waiting queue. Email throttling returns a delivery error to the existing retry and
failure reporting paths; it never reports an undelivered message as successful.
These controls complement edge rate limits and origin isolation; they do not replace
Cloudflare or reverse-proxy DDoS protection. Keep secure cookies enabled when TLS
terminates at a proxy.


## Password policy

Security & sign-in exposes a minimum length (15–128 Unicode characters, default
16). New passwords are normalized to Unicode NFC and checked against an embedded
10,000-entry common-password blocklist, additional predictable full values and
account/service context. There are no mandatory character mixtures or periodic
password expiry. No password is transmitted to a breach-checking service.

All password creation/change/reset endpoints apply the same policy. Existing
passwords remain usable when the policy changes. Newly set passwords use randomly
salted Argon2id (19 MiB, two iterations, one lane); existing bcrypt hashes continue
to verify. Expensive password work is limited to four concurrent operations. This
implements password-policy guidance; it is not a claim of NIST/FIPS certification.
See `internal/authsecurity/passwords/README.md` for the blocklist source/license.

## One-time meeting defaults

Calendar → Meeting defaults chooses the user's default conferencing provider from
currently usable integrations. The existing calendar destination remains the one
booking target; conflict checking reads only calendars enabled for conflicts.
Native Meet/Teams generation requires a compatible booking calendar; Zoom is an
independent connected meeting provider. Changing preferences affects new event
creation and newly minted dynamic links, not saved event types.

Dynamic links use the first selected host as organizer unless overridden. Advanced
settings override title, offered/default durations, location and the organizer's
writable Google/Microsoft calendar for that link only. Other hosts retain their own
calendar destinations. CalDAV uses its existing default destination. Delegated
builders see generic provider labels rather than other hosts' private calendar
names/account addresses. Links retain expiry, atomic single use, and current
creator-role/host-scope checks. New explicit calendar overrides retain provider,
account and calendar identity internally across token refresh and selection changes.


Calendar repair uses the same host-event creator as initial delivery, retaining
privacy flags, custom titles, guest lists and the selected destination. Repair
excludes bookings awaiting approval/payment or being refunded. Only the primary
organizer's provider event invites the client; secondary-host copies block their
calendars without issuing duplicate client invitations.
