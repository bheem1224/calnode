# Single-use links and booking automation

Use a scoped API key. The link operations require `event_types.links` for personal
events or `team_event_types.links` for team events; offering custom slots has its
separate `custom_times` permission. Current team membership and resource scope are
checked by the same handlers used by the UI and MCP.

| Operation | Endpoint |
| --- | --- |
| Create a link | `POST /v1/event-types/{id-or-slug}/single-use-links` |
| List links and their state | `GET /v1/event-types/{id-or-slug}/single-use-links` |
| Revoke an unused link | `DELETE /v1/single-use-links/{token}` |

For a three-day link, send `{"expires_in_hours":72}` when creating it. The default
is **168 hours**, not three days; zero means no expiry. Persist the returned link
`id` against your CRM contact/campaign. The URL is a bearer capability: treat it
as private. Link listings return `expires_at`, `used_at`, `booking_id` and any
`custom_slots`. Renewal creates a **new** link; there is no in-place expiry editor.

For shorter windows, `{"expires_in_seconds":30}` creates a genuinely short-lived
link. Seconds must be an integer from 1 to 604800 (seven days), and cannot be
combined with `expires_in_hours`. This uses the same authoritative expiry checked
by the public booking API and expiry worker. Deploy an image supporting this field
before using it: an older image may ignore unknown fields and use its default.

Subscribe to `single_use_link.booked` and `single_use_link.expired`. Their `data.id`
is the link ID, and a booked notification includes `booking_id`. These events
describe consumption or expiry of the link; inspect the booking if your workflow
needs approval/payment status. An expired unused link has no recipient email:
that association belongs in your CRM when you send it. Revocation is not expiry.

Before sending a reminder, reconcile the link state through the API and check your
campaign has not already been completed or reminded. Then create a fresh link and
send it through your automation's mail channel. Calnode does not automatically
send these sales/follow-up reminders. Existing bookings keep their calendar
invitations independently of the expired/revoked booking link.

## Webhook scopes

`GET /v1/webhooks/scopes` lists authorized choices. Create or patch a webhook with a
`scopes` array. Choices are ORed; an overlapping match queues one notification:

```json
{
  "url": "https://automation.example.com/calnode",
  "events": ["single_use_link.booked", "single_use_link.expired"],
  "scopes": [
    {"type": "user", "id": "YOUR_USER_ID"},
    {"type": "team", "id": "YOUR_TEAM_ID"},
    {"type": "event", "id": "YOUR_EVENT_TYPE_ID"}
  ]
}
```

- `user`: meetings owned/hosted by that account, plus its generated single-use
  links. Selecting another person requires the corresponding role scope.
- `team`: all event types belonging to that team, including future event types.
- `event`: a specific reusable event type.
- `instance`: all authorized instance meetings; requires instance-wide webhook
  and subscribed-data permissions. Omit `id` for this choice.

Scopes never grant additional access. Each delivery/retry intersects the owner's
current permissions with the webhook's saved ceiling, including separate access
to attendee answers, transcripts and notes. Removing team access prevents queued
deliveries from leaking data. A role promotion does not expand an existing
webhook's saved ceiling; recreate the subscription when broader access is wanted.
Legacy `event_type_id` requests remain supported, and cannot be combined with
nonempty multi-scopes. Legacy global hooks retain their account-level behavior.

## Delivery, retries and recovery

Webhook jobs and deliveries are persisted in SQLite. The worker polls about every
five seconds. Requests have a ten-second timeout and use SSRF-safe outbound
networking. Redirects are not followed; configure the final receiver URL.
Transport failures and retryable HTTP errors receive up to **24 attempts within
72 hours**. Backoff is approximately 1 minute, 5 minutes, 15 minutes, 30 minutes,
1 hour, 2 hours, 4 hours, then 6 hours, with jitter. `Retry-After` can postpone a
retry, bounded by the retry window. HTTP 4xx errors other than 408, 425 and 429
are terminal so incorrect URLs, credentials or payloads do not retry endlessly.

Booking creation/confirmation, cancellation, rescheduling and host reassignment
persist their notification source atomically with the state change. The outbox
worker retries fan-out failures independently of calendar/email confirmation.
An expansion replay preserves one logical delivery per subscriber/source event.
Booking events include a stable `event_id` and the transition's `occurred_at`.
Delivery order is not guaranteed across independent retries.

Exhausted deliveries become failed, including a crash during the final attempt.
The delivery log exposes the failure reason and next pending attempt time.
`POST /v1/webhooks/{id}/deliveries/{deliveryId}/retry` deliberately restarts a
failed delivery with the original ID and signed payload. It requires both
`webhooks.update` and `webhooks.deliveries` for the owner/resource scope. The UI
reviews the replay before submitting it; subscription state and current access
are checked again. Manual retry starts a new bounded retry window.

The single-use lifecycle marker and its delivery jobs commit in one transaction.
The worker also recovers consumed links whose booking process stopped before
queueing their notification. Failed queue writes leave no marker, so a later
sweep can retry. Used links are not subsequently emitted as expired. Historical
used links at the time of this migration are not replayed. A newly added webhook
does not replay past lifecycle events.

Your receiver should:

1. Verify `X-Calnode-Signature` against the **raw request body**, using HMAC-SHA256
   with the hex-decoded signing secret. The header is `sha256=<hex digest>`.
2. Persist the request durably and return a 2xx promptly; process it asynchronously.
3. Deduplicate by `X-Calnode-Delivery`. That ID, payload and signature remain
   stable across retries. For durable booking transitions, `event_id` identifies
   the source across subscriptions. Lifecycle actions can additionally use the
   signed `(event, data.id)` pair. Make campaign actions idempotent by link ID.
4. Reconcile outstanding links on a schedule so a prolonged receiver outage or
   exhausted retry does not leave the campaign waiting forever.

A crash after a receiver accepted a request but before the sender recorded success
can cause another attempt. The contract is at-least-once processing with bounded
retries, not exactly-once processing. Do not equate absence of a webhook with
proof that nobody booked.

## Payload field selection

Omitting `fields` retains the legacy default payload. Explicit `fields: []` sends
an empty `data` object; it never falls back to defaults. Field selection applies
to single-use lifecycle events as well as booking events. Authorization metadata
is stored separately, so filtering out identifiers does not disable access checks.

Selectable correlation fields include `event_type_id`, `single_use_link_id`,
`booking_id`, `token`, `expires_at` and `used_at`. On lifecycle events, `data.id`
is the link ID; on booking events, it is the booking ID. Select `single_use_link_id`
on booking events to join the booking to an issued invitation without matching
untrusted names or email text. Only fields relevant to a given event are populated.
The token/URL is a bearer capability and should not be written to routine logs.

## Saved email templates

**Settings → Notifications → Saved email templates** uses the event editor's same
builder, sanitizer and preview renderer. Users can create, search, edit, delete and
choose a default per confirmation/cancellation/reschedule/reminder kind without
first creating an event type. Defaults are copied into **new** event types;
editing/deleting a library item does not silently change existing event emails.

`GET/POST /v1/email-templates`, `PATCH/DELETE /v1/email-templates/{id}` and
`POST /v1/email-templates/preview` are personal, authenticated operations.
