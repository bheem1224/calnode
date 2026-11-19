// Run only against a freshly started disposable localhost instance with an in-memory DB.
import { chromium } from 'playwright';
import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';

const origin = process.env.CALNODE_LOCATION_TEST_URL;
if (!origin || new URL(origin).hostname !== 'localhost') throw new Error('A fresh localhost instance is required');
const status = await (await fetch(origin + '/v1/auth/status')).json();
assert.equal(status.claimed, false, 'Refusing to modify an existing instance');
const output = process.env.CALNODE_SCREENSHOT_DIR || join(tmpdir(), 'calnode-location-screenshots');
await mkdir(output, { recursive: true });
const setup = await fetch(origin + '/v1/setup', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name: 'Location Test', email: 'host@example.test', timezone: 'UTC' }) });
assert.equal(setup.status, 201);
const { api_key: key } = await setup.json();
async function api(path, data, method = 'POST') {
 const response = await fetch(origin + path, { method, headers: { 'Content-Type': 'application/json', 'X-API-Key': key }, body: JSON.stringify(data) });
 assert.ok(response.ok, `${path}: ${response.status} ${await response.clone().text()}`);
 return response.json();
}
const meet = 'https://meet.google.com/abc-defg-hij';
const teams = 'https://teams.microsoft.com/l/meetup-join/test';
const event = await api('/v1/event-types', { slug: 'location-test', name: 'Choose where to meet', duration_minutes: 30, location_type: 'google_meet', location_value: meet, min_notice_minutes: 0, max_future_days: 30 });
await api('/v1/event-types/location-test', { location_options: [{ type: 'teams', value: teams }] }, 'PATCH');
for (let day = 0; day < 7; day++) await api('/v1/availability-rules', { day_of_week: day, start_time: '00:00', end_time: '23:59' });
const browser = await chromium.launch({ headless: true });
const errors = [];
let count = 0;
try {
 for (const width of [1440, 390]) {
  for (const theme of ['light', 'dark']) {
   for (const surface of ['hosted', 'token', 'embed']) {
    const context = await browser.newContext({ viewport: { width, height: width === 390 ? 844 : 900 }, colorScheme: theme });
    await context.addInitScript(theme => localStorage.setItem('fp-theme', theme), theme);
    const page = await context.newPage();
    page.on('pageerror', e => errors.push(`${surface}: ${e.message}`));
    let root = page;
    if (surface === 'embed') {
     const fixture = `<html class="${theme}"><head><meta charset="UTF-8"><style>body{margin:0;padding:16px;background:${theme === 'dark' ? '#0c0d0f' : '#f7f8fa'};box-sizing:border-box}calnode-booking{display:block;max-width:1000px;margin:32px auto}</style></head><body><calnode-booking slug="location-test"></calnode-booking></body></html>`;
     await page.route(origin + '/__location-widget-test', route => route.fulfill({ contentType: 'text/html', body: fixture }));
     await page.goto(origin + '/__location-widget-test');
     await page.addScriptTag({ url: origin + '/embed.js' });
     root = page.locator('calnode-booking');
    } else {
     const token = surface === 'token' ? await api(`/v1/event-types/${event.id}/single-use-links`, {}) : null;
     await page.goto(origin + (token ? '/t/' + token.token : '/book/location-test'));
    }
    await root.locator('.tz-label').waitFor();
    if (surface === 'hosted') {
     await root.locator('#tz-btn').click();
     await root.locator('#tz-search').fill('London');
     assert.equal(await root.locator('.tz-opt').count(), 1);
     await root.locator('.tz-opt').click();
     assert.match(await root.locator('#tz-btn-label').textContent(), /London/);
     await root.locator('#tz-btn').click();
     await page.keyboard.press('Escape');
     assert.equal(await root.locator('#tz-btn').getAttribute('aria-expanded'), 'false');
    }
    await page.screenshot({ path: join(output, `calendar-${surface}-${width}-${theme}.png`), fullPage: true, animations: 'disabled' });
    await root.locator('.cd.available').first().click();
    await root.locator('.slot-btn:not(.taken)').first().click();
    const select = root.locator('#f-location-select');
    const trigger = root.locator('#f-location-select-trigger');
    await trigger.waitFor({ state: 'visible' });
    assert.equal(await select.isVisible(), false);
    assert.deepEqual(await select.locator('option').allTextContents(), ['Choose an option', 'Google Meet', 'Microsoft Teams']);
    const form = surface === 'embed' ? root.locator('form') : root.locator('#booking-form');
    await form.locator('input[autocomplete="name"]').fill('Booking Test');
    await form.locator('input[autocomplete="email"]').fill(`booker${count++}@example.test`);
    let posted = false;
    const onRequest = request => { if (request.method() === 'POST' && request.url().endsWith('/bookings')) posted = true; };
    page.on('request', onRequest);
    await form.locator('button[type="submit"]').click();
    assert.equal(posted, false, 'Missing location submitted a booking');
    const selection = theme === 'dark' ? '1' : '0';
    await trigger.click();
    const options = root.locator('#f-location-select-options');
    await options.waitFor({ state: 'visible' });
    assert.equal(await options.getAttribute('role'), 'listbox');
    assert.equal(await options.getByRole('option').count(), 2);
    const bounds = await options.boundingBox(), triggerBounds = await trigger.boundingBox();
    assert.ok(Math.abs(bounds.width - triggerBounds.width) <= 1, 'Menu width differs from trigger');
    assert.ok(bounds.x >= 0 && bounds.x + bounds.width <= width, 'Menu is clipped horizontally');
    assert.ok(bounds.y >= 0 && bounds.y + bounds.height <= (width === 390 ? 844 : 900), 'Menu is clipped vertically');
    await page.screenshot({ path: join(output, `location-menu-${surface}-${width}-${theme}.png`), fullPage: false, animations: 'disabled' });
    await options.getByRole('option').first().click();
    assert.equal(await select.inputValue(), '0');
    await trigger.click();
    await page.keyboard.press('Escape');
    assert.equal(await trigger.getAttribute('aria-expanded'), 'false');
    await trigger.focus();
    await page.keyboard.press('ArrowDown');
    await page.keyboard.press(selection === '1' ? 'End' : 'Home');
    await page.keyboard.press('Enter');
    assert.equal(await select.inputValue(), selection);
    assert.equal(await trigger.getAttribute('aria-expanded'), 'false');
    const chosen = selection === '1' ? 'Microsoft Teams' : 'Google Meet';
    assert.equal(await root.locator('#summary-location').textContent(), chosen);
    assert.ok(await trigger.evaluate(el => el.getBoundingClientRect().width <= el.parentElement.getBoundingClientRect().width + 1), 'Location field overflows');
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'Horizontal page overflow');
    await page.screenshot({ path: join(output, `${surface}-${width}-${theme}.png`), fullPage: true, animations: 'disabled' });
    const responsePromise = page.waitForResponse(response => response.request().method() === 'POST' && response.url().endsWith('/bookings'));
    await form.locator('button[type="submit"]').click();
    const response = await responsePromise;
    assert.equal(response.status(), 201, await response.text());
    const booked = await response.json();
    const actions = root.locator('.calendar-actions-row');
    await actions.waitFor();
    for (const label of ['Google Calendar', 'Outlook']) {
     const link = actions.getByRole('link', { name: label, exact: true });
     assert.equal(await link.locator('svg[aria-hidden="true"]').count(), 1);
     assert.equal(await link.getAttribute('target'), '_blank');
     assert.equal(await link.getAttribute('rel'), 'noopener noreferrer');
    }
    if (surface === 'hosted' && width === 1440 && theme === 'light') {
     const downloadPromise = page.waitForEvent('download');
     await actions.getByRole('button', { name: 'Download .ics' }).click();
     const download = await downloadPromise;
     assert.equal(download.suggestedFilename(), 'booking.ics');
    }
    await page.screenshot({ path: join(output, `confirmation-${surface}-${width}-${theme}.png`), fullPage: true, animations: 'disabled' });
    assert.equal(booked.location_type, selection === '1' ? 'teams' : 'google_meet');
    assert.equal(booked.location_value, selection === '1' ? teams : meet);
    const submitted = response.request().postDataJSON();
    assert.equal(submitted.selected_location.type, booked.location_type);
    await page.goto(booked.manage_url);
    await page.locator('#summary-location').waitFor();
    assert.equal(await page.locator('#summary-location').textContent(), chosen);
    if (surface === 'hosted') await page.screenshot({ path: join(output, `manage-${width}-${theme}.png`), fullPage: true, animations: 'disabled' });
    await context.close();
   }
  }
 }
 // The admin uses this same public document in a read-only iframe.
 for (const theme of ['light', 'dark']) {
  for (const mode of ['calendar', 'form', 'confirmation']) {
   const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, colorScheme: theme, extraHTTPHeaders: { 'X-API-Key': key } });
   await context.addInitScript(theme => localStorage.setItem('fp-theme', theme), theme);
   const page = await context.newPage();
   page.on('pageerror', e => errors.push(`preview: ${e.message}`));
   await page.route(origin + '/__preview-polish-test', route => route.fulfill({ contentType: 'text/html', body: `<html class="${theme}"><head><meta charset="UTF-8"></head><body style="background:${theme === 'dark' ? '#0c0d0f' : '#f7f8fa'};display:grid;place-items:center;min-height:95vh"><iframe name="preview" title="Booking preview" style="border:0;width:${mode === 'calendar' ? 860 : mode === 'form' ? 720 : 520}px;height:760px"></iframe></body></html>` }));
   await page.goto(origin + '/__preview-polish-test');
   await page.evaluate(({ origin, mode }) => {
    const form = document.createElement('form'); form.method = 'POST'; form.action = origin + '/v1/event-types/location-test/preview'; form.target = 'preview';
    const draft = document.createElement('input'); draft.name = 'draft'; draft.value = JSON.stringify({ mode, notice: 'Preview only — bookings are disabled.' }); form.append(draft); document.body.append(form); form.submit();
   }, { origin, mode });
   const frame = page.frameLocator('iframe');
   if (mode === 'calendar') {
    await frame.locator('.slot-btn').first().waitFor();
    assert.ok(await frame.locator('#tz-btn-label').evaluate(el => el.scrollWidth <= el.clientWidth + 1), 'Timezone is needlessly truncated');
   } else if (mode === 'form') {
    await frame.locator('#form-view:not(.hidden)').waitFor();
    await frame.locator('#f-location-select-trigger').click();
    await frame.getByRole('option', { name: 'Microsoft Teams', exact: true }).click();
    assert.equal(await frame.locator('#summary-location').textContent(), 'Microsoft Teams');
    assert.equal(await frame.locator('#booking-form button[type="submit"]').isDisabled(), true);
   } else {
    await frame.locator('#confirm-view:not(.hidden)').waitFor();
    assert.equal(await frame.locator('.calendar-actions-row button:disabled').count(), 3);
    assert.equal(await frame.locator('.calendar-action-icon').count(), 3);
    assert.equal(await frame.locator('.calendar-actions-row a').count(), 0);
   }
   const previewFrame = page.frames().find(frame => frame.url().includes('/preview'));
   assert.ok(await previewFrame.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'Preview overflows horizontally');
   const cardHeight = await frame.locator('.card').evaluate(el => Math.ceil(el.getBoundingClientRect().height));
   await page.locator('iframe').evaluate((el, height) => { el.style.height = `${height}px`; }, cardHeight);
   await page.locator('iframe').screenshot({ path: join(output, `preview-${mode}-${theme}.png`), animations: 'disabled' });
   await context.close();
  }
 }
 assert.deepEqual(errors, []);
 console.log(`Verified hosted, token, embed, manage and read-only previews in light/dark at desktop/mobile widths. Screenshots: ${output}`);
} finally { await browser.close(); }
