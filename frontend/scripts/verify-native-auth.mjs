// Destructive only to a fresh, disposable localhost instance. Never use a live DB.
import { chromium } from 'playwright';
import { randomBytes } from 'node:crypto';
import assert from 'node:assert/strict';

const origin = process.env.CALNODE_AUTH_TEST_URL;
if (!origin || !['localhost', '127.0.0.1'].includes(new URL(origin).hostname)) {
 throw new Error('CALNODE_AUTH_TEST_URL must identify a fresh localhost test instance');
}
const browser = await chromium.launch({ headless: true });
try {
 const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
 const page = await context.newPage();
 const errors = [];
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
 const cdp = await context.newCDPSession(page);
 await cdp.send('WebAuthn.enable');
 await cdp.send('WebAuthn.addVirtualAuthenticator', { options: { protocol: 'ctap2', transport: 'internal', hasResidentKey: true, hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true } });
 await page.goto(origin + '/admin/settings/account-security');
 await page.getByLabel('Current password', { exact: true }).fill(password);
 await page.getByRole('button', { name: 'Verify', exact: true }).click();
 await page.getByLabel('Device name', { exact: true }).fill('Virtual test passkey');
 await page.getByRole('button', { name: 'Add passkey', exact: true }).click();
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
 console.log('PASS: native enrollment, verified passkey sign-in, password MFA gate, and recovery-code sign-in');
} finally {
 await browser.close();
}
