# Instance security and sign-in

Open **Settings → Security & sign-in**. These policies are stored in the database and take effect without a restart. Provider credentials remain in the Google OAuth and Microsoft OAuth settings pages; disabling scheduler sign-in never disconnects calendars.

For a Microsoft-managed company:

1. Configure the Microsoft application with a specific Entra tenant. Calendar access is separate from sign-in (`openid email profile User.Read`).
2. Keep Microsoft sign-in enabled. Disable Google sign-in if Google is for personal calendar conflicts only.
3. Enable **Require Microsoft sign-in for Members**. Members cannot use passwords, emailed login links, password resets, or Google sign-in. Existing Member sessions from these methods are revoked. Owner/Admin password access has its own switch.
4. Enable automatic enrollment, list approved company domains, and paste the security group's **Object ID** from Entra → Groups. Both domain and transitive group membership must pass. New accounts get the built-in Member role. Failed/unavailable Graph checks deny access rather than bypass the check.
5. Review and save. Verify your password, or sign in again with Microsoft/Google and save within five minutes. The current sign-in method must remain enabled. Policy changes appear in the security audit.

Microsoft Graph's `/me/checkMemberGroups` uses the delegated `User.Read` permission already requested for sign-in. It does not need application-wide directory permissions. See [Microsoft's permission table](https://learn.microsoft.com/en-us/graph/api/directoryobject-checkmembergroups?view=graph-rest-1.0).

## Invitations and existing accounts

An invitation is email-bound, expires after seven days, and can be accepted once. With Microsoft-only Members enabled, the invitation page offers Microsoft sign-in instead of a password form. The verified Microsoft principal must match the invited email. The issuer's current role-grant authority is rechecked at acceptance. Invited accounts are explicitly admitted: they bypass the enrollment security group on initial and subsequent sign-ins, but still follow the configured Microsoft authority and sign-in policy. Invited Admins may choose a password if that option is enabled.

Existing installations preserve their current policy until an administrator changes it. Existing accounts are marked as explicitly admitted because historical enrollment provenance is unavailable; they are not retrospectively assumed to be group-enrolled. New automatically enrolled accounts are tagged separately and have their domain/group checked at each Microsoft sign-in. Turning off automatic enrollment stops new accounts; existing automatic accounts still undergo the configured domain/group check.

Microsoft identities are bound to their stable Graph object ID after successful sign-in. Tenant-verified UPN is used for first-time account matching rather than the mutable Graph `mail` field. Invitations should use the person's Microsoft sign-in address. Changing that address later preserves the existing account through its object ID.

## Offboarding and recovery

Group removal is evaluated at the next sign-in, not continuously during an existing session. For immediate offboarding, archive the account or revoke sessions; archiving also blocks API keys and MCP credentials. Revoking sessions also revokes MCP access tokens. Owner/Admin local password access deliberately does not consult Entra groups. Archive a departed administrator rather than relying on group removal alone.

When Microsoft-only Member access is enabled, assigning a custom permission role does not make someone a password-capable Admin. Demotion immediately makes a password session ineligible. Existing password hashes are retained for rollback, but cannot authenticate while the policy denies that method.

An invite exception does not exempt an account from Microsoft's own tenant/conditional-access rules. Configure corporate MFA and other identity-provider requirements in Entra. Calnode does not bypass them.
