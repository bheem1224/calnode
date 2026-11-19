// Destructive only to a fresh, disposable localhost instance. Never use a live DB.
import { mkdir } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { chromium } from 'playwright';
import { randomBytes } from 'node:crypto';
import assert from 'node:assert/strict';

const origin = process.env.CALNODE_AUTH_TEST_URL;
if (!origin || new URL(origin).hostname !== 'localhost') {
 throw new Error('CALNODE_AUTH_TEST_URL must identify a fresh localhost test instance. IP addresses are not valid passkey relying-party domains.');
}
const browser = await chromium.launch({ headless: true });
let page;
const errors = [];
try {
 const context = await browser.newContext({ reducedMotion:'reduce', viewport: { width: 1440, height: 900 } });
 page = await context.newPage();
 page.on('pageerror', e => errors.push(e.message));
 const api = async (path, body) => {
  const response = await context.request.post(origin + path, { data: body, headers: { Origin: origin } });
  assert.ok(response.ok(), `${path}: ${response.status()} ${await response.text()}`);
  return response;
 };
 const status = await context.request.get(origin + '/v1/auth/status');
 assert.equal((await status.json()).claimed, false, 'Refusing to modify an existing instance');
 const password = randomBytes(24).toString('base64url');
 await api('/v1/auth/claim', { name: 'Security Browser Test', email: 'owner@example.test', password, timezone: 'UTC' });

 // Verify the real security form saves and persists the password policy.
 await page.goto(origin+'/admin/settings/security');
 await page.getByLabel('Minimum password length',{exact:true}).fill('20');
 await page.getByRole('button',{name:'Review & save',exact:true}).click();
 await page.getByLabel('Current password',{exact:true}).fill(password);
 await page.getByRole('button',{name:'Verify',exact:true}).click();
 await page.getByText('Identity verified for five minutes',{exact:true}).waitFor();
 const policySaved=page.waitForResponse(r=>r.url().endsWith('/v1/settings/security')&&r.request().method()==='PUT');
 await page.getByRole('button',{name:'Save security settings',exact:true}).click();
 assert.ok((await policySaved).ok());
 await page.reload();
 assert.equal(await page.getByLabel('Minimum password length',{exact:true}).inputValue(),'20');
 const rejectedPassword=await context.request.post(origin+'/v1/users/me/password',{data:{current_password:password,new_password:'short'},headers:{Origin:origin}});
 assert.equal(rejectedPassword.status(),400);
 const output=process.env.CALNODE_SCREENSHOT_DIR||join(tmpdir(),'calnode-policy-browser');await mkdir(output,{recursive:true});
 for(const theme of ['light','dark']){
  await page.evaluate(theme=>localStorage.setItem('fp-theme',theme),theme);await page.reload();
  await page.getByLabel('Minimum password length',{exact:true}).waitFor();
  await page.screenshot({path:join(output,`password-policy-${theme}.png`),fullPage:true});
 }
 await page.goto(origin+'/admin/calendar');
 await page.getByLabel('Default conferencing',{exact:true}).waitFor();
 const defaultsSaved=page.waitForResponse(r=>r.url().endsWith('/v1/users/me/meeting-defaults')&&r.request().method()==='PUT');
 await page.locator('section').filter({has:page.getByRole('heading',{name:'Meeting defaults',exact:true})}).getByRole('button',{name:'Save',exact:true}).click();
 assert.ok((await defaultsSaved).ok());
 // Actual dynamic-link dialog: organizer follows selection order and overrides persist.
 const invitation=await (await api('/v1/invites',{email:'colleague@example.test'})).json();
 const token=invitation.invite_url.split('/').pop();
 const claim=await fetch(origin+'/v1/invites/'+token+'/claim',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({name:'Colleague',password:randomBytes(24).toString('base64url'),timezone:'UTC'})});assert.equal(claim.status,201);
 await page.goto(origin+'/admin/event-types');
 await page.getByRole('button',{name:'More link options',exact:true}).waitFor();
 await page.keyboard.press('Control+k');
 await page.getByRole('dialog').waitFor();
 await page.getByRole('button',{name:'Dynamic link',exact:true}).click();
 await page.getByRole('checkbox').nth(1).click();
 await page.getByRole('checkbox').nth(0).click();
 await page.getByLabel('Meeting organizer',{exact:true}).waitFor();
 assert.match(await page.getByLabel('Meeting organizer',{exact:true}).innerText(),/Colleague/);
 await page.getByRole('button',{name:/Advanced settings/}).click();
 await page.getByLabel('Meeting title',{exact:true}).fill('Client review');
 await page.getByLabel('Default duration (minutes)',{exact:true}).fill('45');
 await page.getByLabel('Offered durations (comma separated)',{exact:true}).fill('30,45');
 await page.getByLabel('Location',{exact:true}).click();
 await page.getByRole('option',{name:'Video link',exact:true}).click();
 await page.getByRole('listbox').waitFor({state:'hidden'});
 await page.getByLabel('Meeting link, address or phone',{exact:true}).fill('https://example.test/client-review');
 for(const theme of ['light','dark']){
  await page.evaluate(theme=>{document.documentElement.classList.toggle('dark',theme==='dark');document.documentElement.style.colorScheme=theme;},theme);
  await page.waitForFunction(theme=>getComputedStyle(document.querySelector('[role=dialog]')).backgroundColor===(theme==='dark'?'rgb(12, 13, 15)':'rgb(247, 248, 250)'),theme);
  await page.screenshot({path:join(output,`dynamic-advanced-${theme}.png`),animations:'disabled'});
  await page.setViewportSize({width:390,height:844});
  const bounds=await page.getByRole('dialog').boundingBox();
  assert.ok(bounds.x>=-1&&bounds.x+bounds.width<=391,'Dynamic dialog overflows on a phone');
  assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Page overflows on a phone');
  await page.getByRole('button',{name:'Continue',exact:true}).scrollIntoViewIfNeeded();
  await page.screenshot({path:join(output,`dynamic-advanced-${theme}-mobile.png`),animations:'disabled'});
  await page.setViewportSize({width:1440,height:900});

 }
 const minted=page.waitForResponse(r=>r.url().endsWith('/v1/dynamic-links')&&r.request().method()==='POST');
 await page.getByRole('button',{name:'Continue',exact:true}).click();
 const mintedResponse=await minted;assert.equal(mintedResponse.status(),201);const sent=mintedResponse.request().postDataJSON();
 assert.equal(sent.duration_minutes,45);assert.equal(sent.name,'Client review');assert.equal(sent.location_type,'link');assert.equal(sent.location_value,'https://example.test/client-review');
 await page.waitForURL(/\/t\//);await page.getByRole('heading',{name:'Client review',exact:true}).waitFor();
 const cdp = await context.newCDPSession(page);
 await cdp.send('WebAuthn.enable');
 await cdp.send('WebAuthn.addVirtualAuthenticator', { options: { protocol: 'ctap2', transport: 'internal', hasResidentKey: true, hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true } });
 await page.goto(origin + '/admin/settings/account-security');
 await page.getByLabel('Current password', { exact: true }).fill(password);
 const verified = page.waitForResponse(r => new URL(r.url()).pathname === '/v1/auth/reauthenticate' && r.request().method() === 'POST');
 await page.getByRole('button', { name: 'Verify', exact: true }).click();
 const verification = await verified;
 assert.ok(verification.ok(), `Identity verification failed: ${verification.status()} ${await verification.text()}`);
 await page.getByLabel('Device name', { exact: true }).fill('Virtual test passkey');
 const registered = Promise.race([
  page.waitForResponse(r => new URL(r.url()).pathname === '/v1/users/me/security/passkeys/finish' && r.request().method() === 'POST'),
  page.getByRole('alert').waitFor().then(async () => { throw new Error(`Passkey registration failed: ${await page.getByRole('alert').allTextContents()}`); })
 ]);
 const [registration] = await Promise.all([
  registered,
  page.getByRole('button', { name: 'Add passkey', exact: true }).click()
 ]);
 assert.ok(registration.ok(), `Passkey verification failed: ${registration.status()} ${await registration.text()}`);
 await page.getByText('Virtual test passkey', { exact: true }).waitFor();
 const security = await (await context.request.get(origin + '/v1/users/me/security')).json();
 assert.equal(security.passkeys.length, 1, 'Passkey registration did not persist');
 const codes = await (await api('/v1/users/me/security/recovery-codes', {})).json();
 assert.equal(codes.codes.length, 10);
 await api('/v1/auth/logout', {});
 await page.goto(origin + '/admin/login');
 await page.getByRole('button', { name: 'Use a passkey', exact: true }).click();
 await page.waitForURL(url => !url.pathname.includes('/login'));
 assert.equal((await context.request.get(origin + '/v1/users/me')).status(), 200, 'Passkey did not authenticate');
 await api('/v1/auth/logout', {});
 const login = await api('/v1/auth/login/email', { email: 'owner@example.test', password });
 assert.equal(login.headers()['x-calnode-auth-step'], 'mfa');
 assert.equal((await context.request.get(origin + '/v1/users/me')).status(), 401, 'Password bypassed enrolled MFA');
 await api('/v1/auth/mfa/verify', { code: codes.codes[0] });
 assert.equal((await context.request.get(origin + '/v1/users/me')).status(), 200);
 assert.deepEqual(errors, [], 'Browser runtime errors');
 console.log('PASS: password policy persistence, meeting defaults, dynamic-link advanced settings, native enrollment, passkey sign-in, MFA gate, and recovery-code sign-in');
} catch (error) {
 // Restrict diagnostics to the local test page's errors; never dump credentials,
 // WebAuthn payloads, recovery codes, or a full network trace into CI logs.
 if (page) {
  console.error('Native authentication test failed at', page.url());
  console.error('Visible errors:', await page.getByRole('alert').allTextContents());
 }
 if (errors.length) console.error('Browser runtime errors:', errors);
 throw error;
} finally {
 await browser.close();
}
