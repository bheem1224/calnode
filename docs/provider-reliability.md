# Microsoft and Google request reliability

Microsoft Graph and Google Calendar use `internal/providerhttp` beneath their
OAuth transport. One manager is shared by calendar clients and Graph sign-in
helpers for a Calnode database. Recreating a client does not reset its budget.

## Request controls

The defaults are two concurrent requests per connected account, sixteen per OAuth
application, at least 100 ms between account requests and 25 ms between application
requests. Up to 128 requests may be active or waiting in the manager. Response
bodies keep their concurrency slot until closed. A full queue returns a deferred
error instead of allocating an unbounded queue.

Safe requests get at most four attempts within a twelve-second transport budget.
Transient transport failures, HTTP 408/429/500/502/503/504 and Google's specific
quota-related 403 responses use exponential backoff with jitter. Other 4xx errors
are returned. `Retry-After` supports seconds and HTTP dates. Throttling and 503
responses with `Retry-After` pause the affected account; application-scoped Graph
throttling also pauses the application. Cooldowns are saved in `provider_backoff`
(migration 00103) and loaded after restart. Longer waits are handed back to callers;
durable worker retry scheduling honors the provider's retry time.

GET, HEAD and DELETE can be retried. Read-only POSTs (FreeBusy and group checks)
must explicitly opt in. OAuth refresh exchanges and ordinary POST/PATCH requests
are not blindly replayed. Redirects are refused. Graph pagination rejects foreign
origins, repeated pages and excessive page counts before sending credentials.

Expired calendar tokens refresh under a connection-specific lock shared across
clients. Tokens are reloaded from encrypted storage, and rotated credentials are
saved with a compare-and-update rather than deleting the connection. Reconnect and
disconnect take precedence over old clients. Revoked/invalid grants are held for
reconnection rather than hammered by every API call. Concurrent identical busy
reads share only in-flight work; booking commit checks always fetch fresh data.
An unavailable conflict check still blocks booking.

These budgets coordinate one Calnode process, matching the current single-instance
SQLite deployment. They are not a distributed rate limiter for multiple replicas.
Provider quotas may vary; these defaults reduce bursts and react to throttling,
rather than claiming that no request can ever be throttled.

## Duplicate protection

Before a booking host's first calendar write, `calendar_event_intents` pins the
provider, account and calendar for that assignment. Retries cannot switch to a
new destination after a settings change. Event-specific overrides keep their own
account, and co-host copies keep their own calendars and omit client invitees.

- Microsoft creation uses a stable `transactionId` and an extended operation
  property. Recovery looks up that property before creating an event again.
- Google creation uses a stable event ID, a private operation marker and the same
  stable conference request ID. A conflicting ID is read and verified, not
  replaced with a fresh ID.
- Rescheduling serializes read/check/PATCH per event, skips already-applied times,
  and sends `If-Match` when the provider returns an ETag. A stale version is rejected
  rather than blindly writing or sending another update. PATCH is never retried
  by the HTTP transport.
- Cancellation can retry deletion; already-deleted events are successful no-ops.
- Moving a booking to another host gets a new assignment identity. Returning to a
  former host does not reuse the identity of their cancelled calendar event.
  Retrying an already-completed reassignment is a no-op at the API layer too;
  it does not cancel/recreate the current event or start notification mail.

This protects provider event writes and their native invitations. It is not an
exactly-once email-delivery guarantee: delivery acknowledgements can be lost, and
SMTP does not provide an end-to-end idempotency contract. Existing events without
a recorded calendar account retain the existing legacy account-resolution rules.

## Regression verification

Tests use stateful local provider servers, not live calendar load. They count
accepted event creations, reschedules and cancellations while injecting lost
acknowledgements, 503s, conflicts, stale reads, concurrent callers and client
recreation. Additional tests cover rotated/revoked/disconnected tokens, account
overrides, co-host privacy, availability coalescing, full queues, shared quotas,
response-body lifetime, saved cooldowns and Google fallback requests with one
available account slot. CI runs the focused provider concurrency tests with Go's
race detector, in addition to the normal backend and browser suites.

The race detector needs a C compiler; it was added to Linux CI and is not claimed
as locally verified on the Windows development machine. Provider contract tests
do not replace a smoke test against the deployed Microsoft/Google tenants.

## Follow-up sweep: other integrations

The Microsoft/Google retry transport is deliberately not installed around these
side-effecting APIs. They need their own provider-specific recovery contracts.

Two broader calendar lifecycle gaps also remain outside the HTTP retry contract:

- Cancellation reconciliation currently discovers events through their saved
  external IDs. If creation was accepted but every acknowledgement was lost, and
  the booking is cancelled before creation recovery saves the ID, cancellation
  can miss that provider event. The stable operation identity now makes lookup
  possible, but cancellation needs an explicit lookup-and-cleanup recovery path.
- Host reassignment clears the old seat's calendar IDs before asynchronous
  cleanup. If that old-event deletion fails, it is logged but has no independent
  durable cleanup record. A persisted cleanup outbox is needed to survive an
  outage or crash during host changes. Concurrent different reassignments also
  need lifecycle coordination beyond duplicate-create protection.

These are release blockers for an unconditional claim that every calendar
lifecycle operation recovers automatically from any outage. Passing create,
update and delete HTTP fault tests does not demonstrate those missing lifecycle
paths. They should be fixed and exercised before that broader production sign-off.

| Integration | Remaining gap | Release implication |
| --- | --- | --- |
| Zoom (`internal/zoom/zoom.go`) | Meeting creation lacks provider-enforced idempotency and coordinated refresh/throttling. An accepted response lost before storing the meeting ID can create another room on a later job attempt. | Keep automatic Zoom creation out of a release that promises duplicate-safe recovery until stable operation reconciliation is implemented and fault-tested. |
| CalDAV (`internal/caldav/events.go`) | Creation uses a new UID/resource path per call. An ambiguous successful PUT can be followed by a different resource on a later attempt. | Needs a stable UID/path, conditional creation and readback before equivalent retry guarantees apply. |
| Stripe (`internal/stripe/stripe.go`) | Refunds have idempotency keys; checkout-session creation does not. | Keep paid-booking recovery outside the current free-meeting rollout; add stable checkout keys before enabling automatic creation retries. |
| Resend (`internal/mailer/resend.go`) | Sending lacks a stable provider idempotency key and does not propagate Retry-After to worker scheduling. | Ambiguous acceptance can repeat a notification on a later worker attempt. Requires a stable message operation and provider-specific retry handling. |
| SMTP | A lost acknowledgement after message acceptance is ambiguous. | Do not promise exactly-once SMTP delivery. A provider with message-level idempotency is required for that stronger contract. |

LiveKit already derives its room identity from the booking, but its outbound admin
calls do not yet use the shared quota controls. LLM/STT and streaming operations
can incur duplicate cost when replayed and must not receive blanket retries.
Outbound webhooks already use their separate durable retry policy and signing /
target-validation controls; this change does not replace them.

## Deployment boundary

Build and deploy the complete checkout, including migration 00103, before relying
on these protections. Keep the n8n scheduler in TEST MODE until the deployed
cancellation and CRM outage-recovery scenarios pass, received invitations are
checked, and all owned test fixtures are destroyed. This document records a
targeted integration review, not a claim that the entire application has no bugs
or has received an independent security certification.

Provider references: [Microsoft throttling](https://learn.microsoft.com/en-us/graph/throttling),
[Microsoft event transactionId](https://learn.microsoft.com/en-us/graph/api/resources/event?view=graph-rest-1.0),
[Microsoft precondition errors](https://learn.microsoft.com/en-us/graph/errors),
[Google API errors](https://developers.google.com/workspace/calendar/api/guides/errors),
[Google conditional modifications](https://developers.google.com/workspace/calendar/api/guides/version-resources).
