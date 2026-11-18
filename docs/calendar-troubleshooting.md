# Calendar availability and reconnection

Calendar OAuth access is separate from the Calnode browser session. Signing out or letting the browser session expire does not itself disconnect Google or Microsoft calendars.

Both providers request offline access, automatically refresh expired access tokens, encrypt credentials at rest, and persist token updates. Temporary persistence failures are retried on later token calls. Revoked consent, expired refresh tokens, or provider policy changes require the host to reconnect; the application cannot renew revoked authorization.

Google external OAuth apps in Testing status normally receive calendar refresh tokens that expire after seven days. Check the publishing status in Google Cloud if an account needs reconnection weekly. Microsoft organization policies can also force an interactive sign-in.

A successful calendar list does not prove that conflict checking works. The Calendar List and FreeBusy APIs have different behavior; a selected read-only subscription, such as a holiday calendar, may be readable through Events but unavailable through FreeBusy. Calnode falls back to expanded event instances for calendars that return individual FreeBusy errors. Recurring instances, all-day timezones, transparent/cancelled events, and pagination are handled. Any failed or incomplete fallback still blocks booking rather than assuming the host is free.

If booking reports that availability could not be checked:

1. Check the server log at the failed booking's timestamp for the provider error. Do not share access tokens, refresh tokens, client secrets, or full request bodies.
2. Verify the host's selected conflict calendars are still accessible.
3. Reconnect the affected account if Google/Microsoft reports revoked or expired authorization.
4. For recurring expiry, check Google publishing status or the Microsoft tenant's access policy.

References: [Google OAuth refresh-token expiration](https://developers.google.com/identity/protocols/oauth2#expiration), [Microsoft refresh tokens](https://learn.microsoft.com/en-us/entra/identity-platform/refresh-tokens).
