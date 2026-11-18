# Instance security and sign-in

Open **Settings → Security & sign-in**. These policies are stored in the database and take effect without a restart. Provider credentials remain in the Google OAuth and Microsoft OAuth settings pages; disabling scheduler sign-in never disconnects calendars.

For a Microsoft-managed company:

1. Configure the Microsoft application with a specific Entra tenant. Calendar access is separate from sign-in (`openid email profile User.Read`).
2. Keep Microsoft sign-in enabled. Disable Google sign-in if Google is for personal calendar conflicts only.
3. Enable **Require Microsoft sign-in for Members**. Members cannot use passwords, emailed login links, password resets, or Google sign-in. Existing Member sessions from these methods are revoked. Owner/Admin password access has its own switch.
4. Enable automatic enrollment, list approved company domains, and paste the security group's **Object ID** from Entra → Groups. Both domain and transitive group membership must pass. New accounts get the built-in Member role. Failed/unavailable Graph checks deny access rather than bypass the check.
5. Review and save. Verify your enrolled passkey/authenticator, or your password before initial MFA enrollment. A recent Microsoft/Google sign-in also works when no local factor is enrolled. Verification lasts five minutes. The current sign-in method must remain enabled. Policy changes appear in the security audit.

Microsoft Graph's `/me/checkMemberGroups` uses the delegated `User.Read` permission already requested for sign-in. It does not need application-wide directory permissions. See [Microsoft's permission table](https://learn.microsoft.com/en-us/graph/api/directoryobject-checkmembergroups?view=graph-rest-1.0).

## Invitations and existing accounts

An invitation is email-bound, expires after seven days, and can be accepted once. With Microsoft-only Members enabled, the invitation page offers Microsoft sign-in instead of a password form. The verified Microsoft principal must match the invited email. The issuer's current role-grant authority is rechecked at acceptance. Invited accounts are explicitly admitted: they bypass the enrollment security group on initial and subsequent sign-ins, but still follow the configured Microsoft authority and sign-in policy. Invited Admins may choose a password if that option is enabled.

Existing installations preserve their current policy until an administrator changes it. Existing accounts are marked as explicitly admitted because historical enrollment provenance is unavailable; they are not retrospectively assumed to be group-enrolled. New automatically enrolled accounts are tagged separately and have their domain/group checked at each Microsoft sign-in. Turning off automatic enrollment stops new accounts; existing automatic accounts still undergo the configured domain/group check.

Microsoft identities are bound to their stable Graph object ID after successful sign-in. Tenant-verified UPN is used for first-time account matching rather than the mutable Graph `mail` field. Invitations should use the person's Microsoft sign-in address. Changing that address later preserves the existing account through its object ID.

## Offboarding and recovery

Group removal is evaluated at the next sign-in, not continuously during an existing session. For immediate offboarding, archive the account or revoke sessions; archiving also blocks API keys and MCP credentials. Revoking sessions also revokes MCP access tokens. Owner/Admin local password access deliberately does not consult Entra groups. Archive a departed administrator rather than relying on group removal alone.

When Microsoft-only Member access is enabled, assigning a custom permission role does not make someone a password-capable Admin. Demotion immediately makes a password session ineligible. Existing password hashes are retained for rollback, but cannot authenticate while the policy denies that method.

An invite exception does not exempt an account from Microsoft's own tenant/conditional-access rules. Configure corporate MFA and other identity-provider requirements in Entra. Calnode does not bypass them.

## Native MFA and passkeys

Open **Settings → Account security** to register a passkey (recommended), an authenticator app using TOTP, and recovery codes. Passkeys require HTTPS, except localhost during development. Their relying-party ID and allowed origin come from `BASE_URL`, never the request's Host header. Device user verification (PIN/biometric) and discoverable credentials are required. Changing `BASE_URL` to another host can make existing passkeys unusable: arrange backup access before moving domains.

TOTP secrets are encrypted using the existing database encryption key. TOTP time steps cannot be reused, and recovery codes are random, hashed at rest, and single-use. Save recovery codes outside the scheduler; generating another set invalidates the previous set. Register a second passkey before relying on a single device. Removing the last authentication method is refused when MFA is required.

In **Settings → Security & sign-in**, enable MFA for local Owner/Admin sign-in and optionally for native Members. Enroll your own factor first. These switches apply to native authentication; configure SSO MFA/Conditional Access in Microsoft Entra separately. The app does not assert that a recent SSO login independently proves MFA. Disabling Microsoft-only Members enables native authentication for Members too; a custom role does not turn a Member into an instance Admin.

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
