import { afterEach, expect, test, vi } from 'vitest';
import { render, cleanup } from 'vitest-browser-svelte';
import { api } from '$lib/api';
import AccountSecurity from '$lib/components/AccountSecurity.svelte';
import MemberRecoveryDialog from '$lib/components/MemberRecoveryDialog.svelte';

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

test.each(['light', 'dark'])('account security fits and confirms removal in %s mode', async (mode) => {
 const dark = document.documentElement.classList.contains('dark');
 document.documentElement.classList.toggle('dark', mode === 'dark');
 vi.spyOn(api, 'get').mockResolvedValue({ totp_enabled: true, passkeys: [{ id: 'test-key', name: 'Work laptop', created_at: '' }], recovery_codes_remaining: 8, setup_required: false, native_allowed: true, mfa_required: true });
 const remove = vi.spyOn(api, 'del').mockResolvedValue({});
 try {
  const { container } = await render(AccountSecurity);
  await expect.poll(() => container.textContent).toContain('Work laptop');
  const card = container.firstElementChild as HTMLElement;
  expect(card.getBoundingClientRect().width).toBeLessThanOrEqual(672);
  expect(card.scrollWidth).toBeLessThanOrEqual(card.clientWidth);
  const button = [...container.querySelectorAll('button')].find(b => b.textContent?.includes('Remove'))!;
  button.click();
  await expect.poll(() => document.querySelector('[role="alertdialog"]')).not.toBeNull();
  expect(remove).not.toHaveBeenCalled();
 } finally { document.documentElement.classList.toggle('dark', dark); }
});

test('recovery enrollment hides account actions until a factor is enrolled', async () => {
 vi.spyOn(api, 'get').mockResolvedValue({ totp_enabled: false, passkeys: [], recovery_codes_remaining: 0, setup_required: true, native_allowed: true, mfa_required: true });
 const { container } = await render(AccountSecurity);
 await expect.poll(() => container.textContent).toContain('Complete authentication setup');
 expect(container.querySelector('#auth-current-password')).toBeNull();
 expect(container.textContent).not.toContain('Continue to app');
 expect(container.textContent).not.toContain('Generate recovery codes');
 expect(container.querySelector('#passkey-name')).not.toBeNull();
});

test.each(['light', 'dark'])('member MFA recovery keeps identity verification and ticket controls in the %s dialog', async (mode) => {
 const dark = document.documentElement.classList.contains('dark');
 document.documentElement.classList.toggle('dark', mode === 'dark');
 try {
  await render(MemberRecoveryDialog, { props: { open: true, userID: 'member' } });
  const dialog = document.querySelector<HTMLElement>('[role="dialog"]')!;
  expect(dialog.scrollWidth).toBeLessThanOrEqual(dialog.clientWidth);
  expect(dialog.querySelector('input[autocomplete="current-password"]')).not.toBeNull();
  expect(dialog.querySelector('#mfa-recovery-reference')).not.toBeNull();
  expect(dialog.querySelector('[role="switch"]')).not.toBeNull();
  const submit = [...dialog.querySelectorAll('button')].find(b => b.textContent?.includes('Authorize recovery'))!;
  expect(submit.disabled).toBe(true);
 } finally { document.documentElement.classList.toggle('dark', dark); }
});
