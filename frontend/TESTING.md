# Frontend testing

## Visual smoke tests (`pnpm test:visual`)

`src/visual-smoke.test.ts` mounts shadcn-svelte components in a **real browser**
(Vitest 4 browser mode + Playwright/Chromium) and asserts their **computed
styles** — e.g. a checked `Switch` is filled with `--primary`, a Calendar's
selected day is filled, today is underlined.

### The bug class it guards

shadcn-svelte/bits-ui components expose state through `data-*` attributes and
style it with Tailwind variants. There's a silent failure mode:

- Tailwind v4 has **built-in** `data-*` variants: `data-foo:bg-primary` →
  `.…[data-foo]{…}`.
- bits-ui exposes some states as **presence** attributes (`data-selected`,
  `data-today`, `data-outside-month`, `data-disabled`) — the built-in matches
  these, no config needed.
- But on/off and open/closed come through a single **`data-state="…"`**
  attribute. `[data-checked]`/`[data-open]` do **not** match `data-state="checked"`,
  so the utility renders with a non-matching selector and the component is
  **visually unstyled for that state** — while its logic, events, and
  `data-state` attribute are all correct.

These states are remapped in `src/app.css`:

```css
@custom-variant data-checked   (&:where([data-state="checked"]));
@custom-variant data-unchecked (&:where([data-state="unchecked"]));
@custom-variant data-open      (&:where([data-state="open"]));
@custom-variant data-closed    (&:where([data-state="closed"]));
```

**Why a real-browser test (and not a unit test):** the bug is purely CSS. A
jsdom/Vitest unit test asserting "the switch is checked" **passes with the bug
present** — `data-state` is correct; only the compiled CSS doesn't match. Only a
real browser computing real Tailwind output catches it. (There is no reliable
*static* check either: Tailwind always emits a rule, it just may not match — so a
"is every variant declared?" linter gives false confidence and false positives.)

### When to run

After changing anything that affects component styling:

- `src/lib/components/ui/**` (adding/upgrading shadcn-svelte components)
- `src/app.css` (variants, theme tokens)
- bumping `bits-ui`, `shadcn-svelte`, `tailwindcss`, or `vite`

If you add a new stateful shadcn component, add a case to `visual-smoke.test.ts`.

### Adding a case

Mount the component checked/active, then compare computed style to the resolved
token via the `resolveColor('var(--primary)')` probe helper (exact, color-space
agnostic). Query the element that carries the styling classes — for the Calendar
that's the Day button (`[data-bits-day]`), not the parent `<td>` cell.

## Toolchain

- `vitest.config.ts` is standalone (bare `svelte()` + `tailwindcss()`, not
  `sveltekit()`), so components mount in isolation with the real Tailwind
  pipeline. It is **not** the app's `vite.config.ts`.
- `test-setup.ts` imports `app.css` (so real utilities compile) and disables
  transitions/animations (so `getComputedStyle` reads final values).
- Requires the Chromium binary: `pnpm exec playwright install chromium`
  (already downloaded locally; CI must run this).

## Native authentication end-to-end check

`node scripts/verify-native-auth.mjs` exercises a real WebAuthn registration and
sign-in using Chromium's virtual authenticator, then verifies that password login
stops at MFA and a recovery code completes it. CI starts a fresh in-memory server
and runs this after the normal browser suite.

Set `CALNODE_AUTH_TEST_URL` to a **fresh disposable localhost instance** built with
the current frontend. The script refuses an already-claimed instance. It creates
test Owner credentials and a virtual passkey; it never uses an operator's real
passkey, password, browser profile, or production database. Use a separate port
and `DATABASE_URL=sqlite://:memory:`. Normal provider sign-in still needs a staging
tenant test; a virtual authenticator does not prove real-device interoperability.

Use `http://localhost:39137` consistently for `BASE_URL`, browser navigation and
`CALNODE_AUTH_TEST_URL`. **Do not substitute `127.0.0.1`**: Chromium rejects IP
addresses as passkey relying-party domains even though loopback HTTP is otherwise
a secure context. Registration errors now fail the script with the visible error
or server response instead of only timing out waiting for a passkey label.

## Identity provider end-to-end check

Start another **fresh, disposable localhost instance** with the current built
frontend and `DATABASE_URL=sqlite://:memory:`. Run
`CALNODE_IDENTITY_TEST_URL=http://localhost:39140 node scripts/verify-identity-providers.mjs`
(set the environment variable separately in PowerShell). The script refuses an
already-claimed instance and creates its own test Owner and fake app credentials.
It checks the real provider dialog, recent verification, multiple-group ANY/ALL
controls, callback URLs, calendar-only isolation, old settings redirects and the
generic Member SSO policy. It never contacts Google or Microsoft.

Signed-token OIDC integration tests in `internal/handler/identity_providers_test.go`
use a local HTTPS issuer, real signatures, discovery and JWKS. Test real provider
consent, tenant/group policies and calendar refresh against staging accounts before
production rollout; local fakes cannot validate an external app registration.


## Public booking location choices

Start a fresh disposable localhost instance with `DATABASE_URL=sqlite://:memory:`
and run `CALNODE_LOCATION_TEST_URL=http://localhost:39221 node scripts/verify-booking-locations.mjs`.
The script refuses claimed instances, creates fake manual Meet/Teams links and
availability, and verifies actual bookings through hosted, single-use and embed
flows, plus the chosen provider on the manage page. It tests light/dark at desktop
and mobile widths, keyboard and mouse location selection, menu clipping, submitted choices, timezone switching, calendar action logos, the .ics download, read-only previews and horizontal overflow.
No real calendar provider or email service is needed. Screenshots go to the system
temporary directory unless `CALNODE_SCREENSHOT_DIR` is set. Unit tests additionally
verify rescheduling preserves the choice and forged or unsafe locations are rejected.


## Booking feature parity matrix

Build the app binary after building the frontend. Set `CALNODE_TEST_BINARY` to
that binary and run `node frontend/scripts/verify-booking-flows.mjs` from the
repository root. The runner starts and stops its own in-memory instance and a
loopback SMTP capture service; it never accepts a production URL. CI runs this
after the native-authentication browser check using the same built binary.

The matrix books through hosted pages and a genuinely cross-origin Shadow DOM
widget in light/dark at desktop/mobile widths. It exercises all question types,
hidden fields, form ordering, duration choices and locking, location selection,
notes, guest lists, actual emailed verification codes, pending approval, calendar
exports and redirects. Checkout UI rejects an unverified return; Go integration
tests cover pending/paid/refunded checkout states, authorization and shared
REST/MCP approval/rescheduling policy. Set `CALNODE_SCREENSHOT_DIR` to retain
screenshots in a chosen directory; otherwise they go to a temporary directory.

Public DOM controls live in `internal/handler/assets/booking-ui.js`: it is inlined
into hosted/manage pages and prepended to the widget bundle. `BookingLogic` stays
page-only; never reference it from `embed.js`. Form order and metadata come from
Go's `buildBookFormItems`, not a separate set of widget defaults.


The native-auth browser script also saves/reloads the password policy through the
actual security form, checks rejection of a weak password, saves meeting defaults,
and opens the real dynamic-link dialog to verify first-selected organizer, advanced
settings and token navigation. Optional `CALNODE_SCREENSHOT_DIR` retains light/dark
screenshots. The booking matrix additionally asserts delivery of real RSVP-bearing
invitation messages to its local SMTP capture. Provider contract tests inspect actual
HTTP requests for Google/Microsoft create/update/cancel, conferencing and explicit
calendar/account routing; they do not prove delivery to an external mailbox.

## MCP agent configuration and review

`src/agent-review.visual.test.ts` checks company enforcement, escaped proposal
content, confirmation behavior and overflow in both themes. CI also runs
`scripts/verify-agent-operations.mjs` against a fresh in-memory server using
`CALNODE_AGENT_TEST_URL=http://localhost:39144`. It exercises real OAuth PKCE,
the four permission profiles, MCP calls, browser approval, inactive/private event
creation and replay rejection. The runner refuses claimed or non-localhost
instances. Optional `CALNODE_SCREENSHOT_DIR` retains light/dark screenshots;
screenshots select the actual stored theme and wait for its text colors to settle.
See `docs/mcp-agents.md` for policy and import limitations.

The same real-server run imports `scripts/verify-notification-settings.mjs` to
create/edit/delete saved templates through Notifications, check sanitized HTML and
new-event defaults, and create/edit a webhook with multiple scopes. Light/dark
screenshots include both settings pages. `notification-settings.visual.test.ts`
checks the shared template builder and multi-select picker in both themes.

The booking matrix asserts at most two visitor-local time periods on hosted and
embedded pages and exercises the manage/reschedule picker at desktop/mobile sizes
in both themes. Backend lifecycle tests simulate queue failures, restart recovery,
signed retries, backoff, changed scopes and overlapping selections. Mail capture
tests check saved HTML and reply-to on cancellation, reschedule and reminder emails.
