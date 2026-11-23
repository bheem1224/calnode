// Real-server regression matrix. Starts an isolated in-memory app and a loopback
// SMTP capture server; never accepts a URL or touches an existing instance.
import { chromium } from 'playwright';
import assert from 'node:assert/strict';
import { createServer as tcpServer } from 'node:net';
import { createServer as httpServer } from 'node:http';
import { spawn } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { mkdtemp, mkdir } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
const binary = process.env.CALNODE_TEST_BINARY;
if (!binary)
    throw new Error('Build a test binary and set CALNODE_TEST_BINARY');
const temp = await mkdtemp(join(tmpdir(), 'calnode-booking-flows-'));
const screenshots = process.env.CALNODE_SCREENSHOT_DIR || join(temp, 'screenshots');
await mkdir(screenshots, { recursive: true });
const listen = server => new Promise(resolve => server.listen(0, '127.0.0.1', () => resolve(server.address().port)));
const messages = [];
const sockets = new Set();
const smtp = tcpServer(socket => {
    sockets.add(socket);
    socket.on('close', () => sockets.delete(socket));
    socket.write('220 localhost test relay\r\n');
    let buffer = '', data = false, raw = '', recipients = [];
    socket.on('data', chunk => {
        buffer += chunk.toString();
        let end;
        while ((end = buffer.indexOf('\r\n')) >= 0) {
            const line = buffer.slice(0, end);
            buffer = buffer.slice(end + 2);
            if (data) {
                if (line === '.') {
                    messages.push({ raw, recipients });
                    data = false;
                    raw = '';
                    recipients = [];
                    socket.write('250 queued\r\n');
                }
                else
                    raw += line + '\r\n';
                continue;
            }
            if (/^EHLO|^HELO/i.test(line))
                socket.write('250-localhost\r\n250 SIZE 10485760\r\n');
            else if (/^RCPT TO:/i.test(line)) {
                recipients.push(line.toLowerCase());
                socket.write('250 OK\r\n');
            }
            else if (/^DATA$/i.test(line)) {
                data = true;
                socket.write('354 end with dot\r\n');
            }
            else if (/^QUIT$/i.test(line)) {
                socket.end('221 bye\r\n');
            }
            else
                socket.write('250 OK\r\n');
        }
    });
});
const smtpPort = await listen(smtp);
const reservation = tcpServer();
const port = await listen(reservation);
await new Promise(resolve => reservation.close(resolve));
const origin = `http://localhost:${port}`;
const env = { ...process.env, PORT: String(port), BASE_URL: origin, PUBLIC_BASE_URL: origin, DATABASE_URL: 'sqlite://:memory:', DATA_DIR: temp, DEMO_MODE: 'false', CALNODE_ENCRYPTION_KEY: randomBytes(32).toString('hex'), CALNODE_RECOVERY_SECRET: '', EMAIL_SMTP_HOST: '127.0.0.1', EMAIL_SMTP_PORT: String(smtpPort), EMAIL_SMTP_CONNECT_HOST: '', EMAIL_SMTP_CONNECT_PORT: '', EMAIL_SMTP_USER: '', EMAIL_SMTP_PASS: '', EMAIL_SMTP_TLS: 'false', EMAIL_SMTP_STARTTLS: 'false', EMAIL_FROM_ADDRESS: 'scheduler@example.test', GOOGLE_CLIENT_ID: '', GOOGLE_CLIENT_SECRET: '', MICROSOFT_CLIENT_ID: '', MICROSOFT_CLIENT_SECRET: '', MICROSOFT_CALENDAR_CLIENT_ID: '', MICROSOFT_CALENDAR_CLIENT_SECRET: '', ZOOM_CLIENT_ID: '', ZOOM_CLIENT_SECRET: '', RATE_LIMIT_BOOKING_RPM: '500', RATE_LIMIT_PUBLIC_RPM: '1000', RATE_LIMIT_BOT_RPM: '1000', RATE_LIMIT_SLOTS_RPM: '1000' };
const app = spawn(resolve(binary), [], { env, windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'] });
let logs = '';
for (const stream of [app.stdout, app.stderr])
    stream.on('data', chunk => logs = (logs + chunk).slice(-12000));
let browser, fixture;
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
try {
    let ready = false;
    for (let n = 0; n < 120; n++) {
        if (app.exitCode !== null)
            throw new Error('Test app exited: ' + logs);
        try {
            if ((await fetch(origin + '/v1/auth/status')).ok) {
                ready = true;
                break;
            }
        }
        catch { }
        await pause(250);
    }
    assert.ok(ready, 'Test server did not start');
    assert.equal((await (await fetch(origin + '/v1/auth/status')).json()).claimed, false);
    const setup = await (await fetch(origin + '/v1/setup', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name: 'Booking Matrix', email: 'host@example.test', timezone: 'UTC' }) })).json();
    const key = setup.api_key;
    async function api(path, data, method = 'POST') { const response = await fetch(origin + path, { method, headers: { 'Content-Type': 'application/json', 'X-API-Key': key }, ...(data === undefined ? {} : { body: JSON.stringify(data) }) }); const body = await response.json(); assert.ok(response.ok, `${path}: ${response.status} ${JSON.stringify(body)}`); return body; }
    for (let day = 0; day < 7; day++)
        await api('/v1/availability-rules', { day_of_week: day, start_time: '00:00', end_time: '23:59' });
    async function event(slug, settings = {}) {
        const e = await api('/v1/event-types', { slug, name: slug, duration_minutes: 30, min_notice_minutes: 0, max_future_days: 30, location_type: 'google_meet', location_value: 'https://meet.google.com/abc-defg-hij' });
        if (Object.keys(settings).length)
            await api('/v1/event-types/' + slug, settings, 'PATCH');
        return e;
    }
    const advanced = await event('matrix-advanced', { is_public: false, allow_multiple_durations: true, duration_options: [15, 30, 45], location_options: [{ type: 'teams', value: 'https://teams.microsoft.com/l/meetup-join/test' }], booking_form_config: JSON.stringify([{ key: 'notes' }, { key: 'name' }, { key: 'email' }, { key: 'phone', hidden: true }, { key: 'guests' }]) });
    const hiddenTeam = await api('/v1/teams', { name: 'Hidden Team', slug: 'hidden-team' });
    await event('matrix-hidden-team', { is_public: false, team_id: hiddenTeam.id, routing_mode: 'collective' });
    const types = [['text', null], ['number', null], ['url', null], ['address', null], ['phone', null], ['multiple_emails', null], ['select', ['Alpha', 'Beta']], ['radio_group', ['Alpha', 'Beta']], ['multiselect', ['Alpha', 'Beta']], ['checkbox_group', ['Alpha', 'Beta']], ['checkbox', null]];
    const questions = [];
    for (const [type, options] of types)
        questions.push(await api('/v1/event-types/matrix-advanced/questions', { label: 'Question ' + type, type, options: options || undefined, required: true, identifier: type === 'text' ? 'request_context' : undefined, lock_if_prefilled: type === 'text' }));
    await api('/v1/event-types/matrix-advanced/questions', { label: 'Hidden question', type: 'text', hidden: true, identifier: 'source', lock_if_prefilled: true });
    await event('matrix-pending', { requires_confirmation: true, confirm_mode: 'always' });
    await event('matrix-verify', { is_public: false, require_email_verification: true });
    await event('matrix-redirect', { redirect_url: origin + '/__booking-complete' });
    fixture = httpServer((req, res) => {
        const url = new URL(req.url, 'http://localhost');
        const slug = url.searchParams.get('slug') || 'matrix-advanced';
        const theme = url.searchParams.get('theme') === 'dark' ? 'dark' : 'light';
        res.setHeader('Content-Type', 'text/html; charset=utf-8');
        res.end(`<!doctype html><html class="${theme}"><head><meta charset="UTF-8"><style>body{margin:0;padding:16px;background:${theme === 'dark' ? '#0c0d0f' : '#f7f8fa'};box-sizing:border-box}calnode-booking{display:block;max-width:1000px;margin:24px auto}</style><script>window.module={exports:{hostSite:true}};</script><script src="${origin}/embed.js" defer></script></head><body><calnode-booking slug="${slug}" prefill="request_context=Initial%20context&amp;source=campaign"></calnode-booking></body></html>`);
    });
    const fixturePort = await listen(fixture);
    browser = await chromium.launch({ headless: true });
    let lastCompleted;
    let bookings = 0;
    const errors = [];
    for (const width of [1440, 390])
        for (const theme of ['light', 'dark'])
            for (const surface of ['hosted', 'embed']) {
                const context = await browser.newContext({ viewport: { width, height: width === 390 ? 844 : 900 }, colorScheme: theme, timezoneId: 'UTC' });
                await context.addInitScript(theme => localStorage.setItem('fp-theme', theme), theme);
                const page = await context.newPage();
                page.on('pageerror', error => errors.push(`${surface}: ${error.message}`));
                let root = page;
                async function open(slug) {
                    await page.goto(surface === 'hosted' ? origin + '/book/' + slug + '?request_context=Initial%20context&source=campaign' : `http://localhost:${fixturePort}/?slug=${slug}&theme=${theme}`);
                    root = surface === 'hosted' ? page : page.locator('calnode-booking');
                    await root.locator('.cd.available').first().waitFor();
                }
                async function choose() {
                    // Tomorrow has both halves of the day regardless of the runner clock.
                    await root.locator('.cd.available').nth(1).click();
                    await root.locator('.slot-btn:not(.taken)').first().waitFor();
                    const periods=root.locator('[data-period]');
                    assert.ok(await periods.count()<=2,'Booking slots regressed to three periods');
                    assert.ok(!(await periods.allTextContents()).some(label=>label.includes('Afternoon')),'Unexpected third period');
                    await page.screenshot({path:join(screenshots,`calendar-${surface}-${width}-${theme}.png`),fullPage:true,animations:'disabled'});
                    await root.locator('.slot-btn:not(.taken)').first().click();
                }
                async function identity(prefix) { await root.locator('#f-name').fill('Booking Test'); const email = `${prefix}-${bookings++}@example.test`; await root.locator('#f-email').fill(email); return email; }
                async function submit() { const result = page.waitForResponse(response => response.request().method() === 'POST' && response.url().endsWith('/v1/bookings')); await root.locator('button[type="submit"]').click(); const response = await result; assert.equal(response.status(), 201, await response.text()); return { body: await response.json(), sent: response.request().postDataJSON() }; }
                await open('matrix-hidden-team');
                await choose();
                await identity('hidden-team');
                const teamBooking = await submit();
                assert.ok(teamBooking.body.id, 'Hidden team event did not create a booking');
                await root.getByText('Booking confirmed', { exact: true }).waitFor();
                await page.screenshot({ path: join(screenshots, `hidden-team-${surface}-${width}-${theme}.png`), fullPage: true, animations: 'disabled' });
                await open('matrix-advanced');
                await root.locator('.dur-pill').filter({ hasText: '15' }).click();
                await choose();
                assert.equal(await root.locator('.dur-pill:visible').count(), 0, 'Duration remains editable after choosing a time');
                await identity('advanced');
                await root.locator('#f-location-select-trigger').click();
                await root.getByRole('option', { name: 'Microsoft Teams', exact: true }).click();
                await root.locator('#f-notes').fill('Notes from the configured form');
                assert.equal(await root.locator('#f-phone').count(), 0, 'Hidden system phone field was rendered');
                assert.equal(await root.getByText('Hidden question', { exact: true }).isVisible(), false);
                for (const q of questions) {
                    const field = root.locator(`[data-qid="${q.id}"]`);
                    if (q.type === 'text') {
                        assert.equal(await field.locator('textarea').inputValue(), 'Initial context');
                        assert.equal(await field.locator('textarea').getAttribute('readonly'), '');
                    }
                    else if (q.type === 'checkbox')
                        await field.locator('input').check();
                    else if (['radio_group', 'checkbox_group', 'multiselect'].includes(q.type))
                        await field.locator('input').first().check();
                    else if (q.type === 'select')
                        await field.locator('select').selectOption('Beta');
                    else
                        await field.locator('input:not([type=search]),textarea').fill({ number: '42', url: 'https://example.test', phone: '+12025550123', multiple_emails: 'one@example.test,two@example.test' }[q.type] || 'Answer');
                }
                await root.locator(`[data-qid="${questions.find(q => q.type === 'phone').id}"] .phone-country`).waitFor();
                await root.locator('#guests-toggle-btn').click();
                await root.locator('#guests-tab-list').click();
                await root.locator('#guests-list-input').fill('guest-one@example.test,guest-two@example.test');
                assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'Page overflows horizontally');
                await page.screenshot({ path: join(screenshots, `form-${surface}-${width}-${theme}.png`), fullPage: true, animations: 'disabled' });
                const done = await submit();
                lastCompleted = done.body;
                const recipient=done.sent.email;
                let invitation;
                for(let retry=0;retry<100;retry++){
                    invitation=messages.find(m=>m.recipients.some(r=>r.includes(recipient.toLowerCase()))&&/text\/calendar/i.test(m.raw));
                    if(invitation)break;
                    await pause(50);
                }
                assert.ok(invitation,'Booking invitation was not delivered to SMTP capture');
                assert.match(invitation.raw,/method=["']?REQUEST/i,'Invitation lacks RSVP method');

                assert.equal(done.sent.duration_minutes, 15);
                assert.equal(done.body.location_type, 'teams');
                assert.equal(done.body.notes, 'Notes from the configured form');
                assert.equal(done.body.guests.length, 2);
                assert.equal(done.sent.answers.filter(a => a.value).length, questions.length + 1);
                await root.locator('.calendar-actions-row').waitFor();
                assert.equal(await root.locator('.calendar-actions-row a').count(), 2);
                await page.screenshot({ path: join(screenshots, `confirmation-${surface}-${width}-${theme}.png`), fullPage: true, animations: 'disabled' });
                await open('matrix-pending');
                await choose();
                await identity('pending');
                const pending = await submit();
                assert.equal(pending.body.status, 'pending');
                await root.getByText('Booking request sent', { exact: true }).waitFor();
                assert.equal(await root.locator('.calendar-actions-row a').count(), 0);
                assert.equal(await root.locator('.calendar-actions-row button:disabled').count(), 3);
                await open('matrix-verify');
                await choose();
                const email = await identity('verify');
                await root.locator('#send-code-btn').click();
                let mail;
                for (let n = 0; n < 80; n++) {
                    mail = messages.find(message => message.recipients.some(recipient => recipient.includes(email)));
                    if (mail)
                        break;
                    await pause(50);
                }
                assert.ok(mail, 'Verification email was not delivered');
                const code = mail.raw.match(/\b\d{6}\b/)?.[0];
                assert.ok(code, 'Verification code missing from email');
                await root.locator('#verify-code-input').fill(code === '000000' ? '000001' : '000000');
                const rejected = page.waitForResponse(response => response.url().endsWith('/verify-email/check'));
                await root.locator('#check-code-btn').click();
                assert.equal((await rejected).status(), 400);
                await root.locator('#verify-code-input').fill(code);
                await root.locator('#check-code-btn').click();
                await root.getByText('Email verified', { exact: true }).waitFor();
                const verified = await submit();
                assert.ok(verified.sent.verification_token);
                assert.equal(verified.body.status, 'confirmed');
                await context.close();
            }

    assert.ok(lastCompleted.manage_url,'Completed booking omitted its manage URL');
    // A hidden event remains directly bookable; its one-time URL must still be
    // consumed independently, and the resulting booking must remain manageable.
    const privateEvent = await event('matrix-private', {is_public:false});
    assert.equal((await fetch(origin+'/v1/event-types/matrix-private/slots')).status,200);
    const privateLink = await api('/v1/event-types/'+privateEvent.id+'/single-use-links',{expires_in_hours:72});
    const privateDay = new Date(Date.now()+3*86400000).toISOString().slice(0,10);
    const privateSlots = await (await fetch(origin+'/v1/t/'+privateLink.token+'/slots?from='+privateDay+'&to='+privateDay+'&tz=UTC')).json();
    assert.ok(privateSlots.slots.length,'Private link has no initial availability');
    let managedBooking = await api('/v1/t/'+privateLink.token+'/bookings',{start_at:privateSlots.slots[0].start,name:'Private attendee',email:'private@example.test',timezone:'UTC'});
    assert.equal((await fetch(origin+'/v1/t/'+privateLink.token+'/slots')).status,410);
    for(const width of [1440,390])for(const theme of ['light','dark']){
        const context=await browser.newContext({viewport:{width,height:900},colorScheme:theme,timezoneId:'UTC'});
        await context.addInitScript(theme=>localStorage.setItem('fp-theme',theme),theme);
        const page=await context.newPage();page.on('pageerror',error=>errors.push('manage: '+error.message));
        await page.goto(managedBooking.manage_url);
        const availability=page.waitForResponse(r=>r.url().includes('/manage/')&&r.url().includes('/slots?'));
        await page.locator('#reschedule-btn').click();
        assert.equal((await availability).status(),200,'Private manage availability rejected');
        // The preceding bookings consume tomorrow's morning; choose a fresh day.
        await page.locator('.cd.available').nth(2).click();await page.locator('.slot-btn:not(.taken)').first().waitFor();
        assert.deepEqual(await page.locator('[data-period]').allTextContents(),['Morning','Evening']);
        await page.locator('[data-period="1"]').click();await page.locator('.slot-btn:not(.taken)').first().waitFor();
        assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Manage page overflows horizontally');
        await page.screenshot({path:join(screenshots,`calendar-manage-${width}-${theme}.png`),fullPage:true,animations:'disabled'});
        await page.locator('.slot-btn:not(.taken)').first().click();await page.locator('#confirm-reschedule-btn').waitFor();
        const moved=page.waitForResponse(r=>r.request().method()==='POST'&&r.url().endsWith('/reschedule'));
        await page.locator('#confirm-reschedule-btn').click();
        const movedResponse=await moved;assert.equal(movedResponse.status(),200,await movedResponse.text());
        managedBooking=await movedResponse.json();
        await page.locator('#success-reschedule-view').waitFor();
        assert.equal(Date.parse(managedBooking.end_at)-Date.parse(managedBooking.start_at),30*60000,'Rescheduling changed duration');
        await page.screenshot({path:join(screenshots,`rescheduled-private-${width}-${theme}.png`),fullPage:true,animations:'disabled'});
        await context.close();
    }
    // UI-only failure injection: a failed availability read must explain the error,
    // rather than presenting every date as genuinely unavailable.
    for(const theme of ['light','dark']){
        const context=await browser.newContext({viewport:{width:390,height:844},colorScheme:theme});
        await context.addInitScript(theme=>localStorage.setItem('fp-theme',theme),theme);
        const page=await context.newPage();
        await page.route('**/manage/*/slots?**',route=>route.fulfill({status:503,contentType:'application/json',body:'{"error":"test unavailable"}'}));
        await page.goto(managedBooking.manage_url);await page.locator('#reschedule-btn').click();
        await page.getByText('Could not load times. Please try again.',{exact:true}).waitFor();
        assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Manage error overflows');
        await page.screenshot({path:join(screenshots,`availability-error-390-${theme}.png`),fullPage:true,animations:'disabled'});
        await context.close();
    }
    // Real navigation honors the configured redirect, on both booking surfaces.
    for (const surface of ['hosted', 'embed']) {
        const page = await browser.newPage();
        await page.route(origin + '/__booking-complete', route => route.fulfill({ contentType: 'text/html', body: 'Booking complete' }));
        await page.goto(surface === 'hosted' ? origin + '/book/matrix-redirect' : `http://localhost:${fixturePort}/?slug=matrix-redirect`);
        const root = surface === 'hosted' ? page : page.locator('calnode-booking');
        await root.locator('.cd.available').first().click();
        await root.locator('.slot-btn:not(.taken)').first().click();
        await root.locator('#f-name').fill('Redirect');
        await root.locator('#f-email').fill(`redirect-${surface}@example.test`);
        await root.locator('button[type="submit"]').click();
        await page.waitForURL(origin + '/__booking-complete');
        await page.close();
    }
    const paid = await browser.newPage();
    paid.on('pageerror', error => errors.push(error.message));
    await paid.goto(origin + '/book/matrix-advanced?paid=1&session_id=invalid');
    await paid.getByText('Payment confirmation is not available yet. Check your email or refresh this page shortly.', { exact: true }).waitFor();
    assert.equal(await paid.locator('.calendar-actions-row').count(), 0, 'Unverified checkout offers calendar exports');
    // Go tests verify the paid-state endpoint against the DB. This UI-only stub
    // exercises its processing -> paid response without charging a real card.
    let polls = 0;
    await paid.route(origin + '/v1/book/matrix-advanced/checkout?session_id=cs_ui_regression', route => {
        const waiting = polls++ < 1;
        return route.fulfill({ status: waiting ? 202 : 200, contentType: 'application/json', body: JSON.stringify(waiting ? { status: 'processing' } : { ...lastCompleted, payment_status: 'paid' }) });
    });
    await paid.goto(origin + '/book/matrix-advanced?paid=1&session_id=cs_ui_regression');
    await paid.getByText('Booking confirmed', { exact: true }).waitFor();
    assert.equal(await paid.locator('.calendar-actions-row a').count(), 2);
    assert.ok(polls >= 2, 'Checkout skipped durable confirmation polling');
    await paid.close();
    assert.deepEqual(errors, []);
    console.log(`Verified 32 configured booking flows including hidden personal/team events, redirects and checkout failure handling across hosted/embed plus manage rescheduling, light/dark, desktop/mobile. Screenshots: ${screenshots}`);
}
catch (error) {
    console.error(logs);
    throw error;
}
finally {
    if (browser)
        await browser.close();
    app.kill();
    if (app.exitCode === null)
        await Promise.race([new Promise(resolve => app.once('exit', resolve)), pause(3000)]);
    if (fixture)
        await new Promise(resolve => fixture.close(resolve));
    for (const socket of sockets)
        socket.destroy();
    await new Promise(resolve => smtp.close(resolve));
}
