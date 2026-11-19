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
     const fixture = `<html class="${theme}"><head><style>body{margin:0;padding:16px;background:${theme === 'dark' ? '#0c0d0f' : '#f7f8fa'};box-sizing:border-box}calnode-booking{display:block;max-width:1000px;margin:32px auto}</style></head><body><calnode-booking slug="location-test"></calnode-booking></body></html>`;
     await page.route(origin + '/__location-widget-test', route => route.fulfill({ contentType: 'text/html', body: fixture }));
     await page.goto(origin + '/__location-widget-test');
     await page.addScriptTag({ url: origin + '/embed.js' });
     root = page.locator('calnode-booking');
    } else {
     const token = surface === 'token' ? await api(`/v1/event-types/${event.id}/single-use-links`, {}) : null;
     await page.goto(origin + (token ? '/t/' + token.token : '/book/location-test'));
    }
    await root.locator('.cd.available').first().click();
    await root.locator('.slot-btn:not(.taken)').first().click();
    const select = root.locator('#f-location-select');
    await select.waitFor({ state: 'visible' });
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
    await select.selectOption(selection);
    const chosen = selection === '1' ? 'Microsoft Teams' : 'Google Meet';
    assert.equal(await root.locator('#summary-location').textContent(), chosen);
    assert.ok(await select.evaluate(el => el.getBoundingClientRect().width <= el.parentElement.getBoundingClientRect().width + 1), 'Location field overflows');
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'Horizontal page overflow');
    await page.screenshot({ path: join(output, `${surface}-${width}-${theme}.png`), fullPage: true, animations: 'disabled' });
    const responsePromise = page.waitForResponse(response => response.request().method() === 'POST' && response.url().endsWith('/bookings'));
    await form.locator('button[type="submit"]').click();
    const response = await responsePromise;
    assert.equal(response.status(), 201, await response.text());
    const booked = await response.json();
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
 assert.deepEqual(errors, []);
 console.log(`Verified hosted, token, embed and manage locations in light/dark at desktop/mobile widths. Screenshots: ${output}`);
} finally { await browser.close(); }
