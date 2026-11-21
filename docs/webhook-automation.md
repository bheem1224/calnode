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
networking. A non-2xx response or transport failure is retried with a one-minute
delay, then a five-minute delay: **three total attempts** by default. Exhausted
deliveries become failed and remain inspectable in the webhook delivery log/API.
This is bounded retry, not an indefinite delivery guarantee.

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
   stable across retries. Also make campaign actions idempotent by link ID.
4. Reconcile outstanding links on a schedule so a prolonged receiver outage or
   exhausted retry does not leave the campaign waiting forever.

A crash after a receiver accepted a request but before the sender recorded success
can cause another attempt. The contract is at-least-once processing with bounded
retries, not exactly-once processing. Do not equate absence of a webhook with
proof that nobody booked.

## Saved email templates

**Settings → Notifications → Saved email templates** uses the event editor's same
builder, sanitizer and preview renderer. Users can create, search, edit, delete and
choose a default per confirmation/cancellation/reschedule/reminder kind without
first creating an event type. Defaults are copied into **new** event types;
editing/deleting a library item does not silently change existing event emails.

`GET/POST /v1/email-templates`, `PATCH/DELETE /v1/email-templates/{id}` and
`POST /v1/email-templates/preview` are personal, authenticated operations.
