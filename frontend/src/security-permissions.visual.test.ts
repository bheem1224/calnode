import { afterEach, expect, test, vi } from 'vitest';
import { cleanup, render } from 'vitest-browser-svelte';
import { page } from 'vitest/browser';
import { api, type PermissionGrant } from '$lib/api';
import en from '$lib/i18n/en.json';
import AccountSecurity from '$lib/components/AccountSecurity.svelte';
import AgentReviewPreferences from '$lib/components/AgentReviewPreferences.svelte';
import PermissionEditor from '$lib/components/PermissionEditor.svelte';
import SecuritySettings from './routes/settings/security/+page.svelte';

afterEach(() => { cleanup(); vi.restoreAllMocks(); document.documentElement.classList.remove('dark'); });

const policy = { password_min_length:16,mcp_review_enforced:false,require_admin_mfa:false,require_member_mfa:false,session_hours:12,dynamic_link_days:7,members_sso_only:false,admin_password_enabled:true };
function mockSecurity() {
 vi.spyOn(api,'get').mockImplementation(async path => {
  if(path==='/v1/users/me/security')return {native_allowed:true,setup_required:false,mfa_required:false,totp_enabled:false,passkeys:[{id:'passkey',name:'Work laptop',created_at:''}],recovery_codes_remaining:10} as never;
  if(path==='/v1/users/me/identities')return {items:[{id:'entra',name:'Company SSO',linked:true}]} as never;
  if(path==='/v1/users/me/mcp-preferences')return {review_required:true,enforced:false,effective_review_required:true} as never;
  if(path==='/v1/settings/security')return {policy:{...policy}} as never;
  if(path==='/v1/settings/security/operations')return {failed_jobs:[],calendar_errors:[],failed_confirmations:0,pending_jobs:0} as never;
  return {items:[]} as never;
 });
}
function assertFits(container: HTMLElement) {
 expect(container.scrollWidth).toBeLessThanOrEqual(document.documentElement.clientWidth);
 expect(document.documentElement.scrollWidth).toBeLessThanOrEqual(innerWidth);
}

test.each(['light','dark'])('account security keeps status visible and reveals instructions on demand in %s',async theme=>{
 document.documentElement.classList.toggle('dark',theme==='dark');mockSecurity();
 const {container}=await render(AccountSecurity);
 await expect.poll(()=>container.textContent).toContain('Work laptop');
 expect(container.textContent).toContain('10 codes remaining');
 expect(container.textContent).not.toContain(en.nativeAuth.description);
 expect(container.textContent).not.toContain(en.nativeAuth.verifyHint);
 const help=container.querySelector<HTMLButtonElement>('button[aria-label="Help: '+en.nativeAuth.verifyTitle+'"]')!;
 help.focus();
 await expect.poll(()=>document.querySelector('[data-slot="tooltip-content"]')?.textContent).toContain(en.nativeAuth.verifyHint);
 assertFits(container);
});

test.each(['light','dark'])('security policy remains editable and reviews session effects before saving in %s',async theme=>{
 document.documentElement.classList.toggle('dark',theme==='dark');mockSecurity();
 const put=vi.spyOn(api,'put').mockResolvedValue({policy:{...policy,password_min_length:24}});
 const {container}=await render(SecuritySettings);
 await expect.poll(()=>container.querySelector<HTMLInputElement>('#password-minimum')?.value).toBe('16');
 expect(container.textContent).not.toContain(en.passwordPolicy.description);
 expect(container.textContent).not.toContain(en.identity.sessionHint);
 const input=container.querySelector<HTMLInputElement>('#password-minimum')!;
 input.value='24';input.dispatchEvent(new Event('input',{bubbles:true}));
 [...container.querySelectorAll('button')].find(b=>b.textContent?.trim()===en.securitySettings.save)!.click();
 await expect.poll(()=>document.querySelector('[role="dialog"]')?.textContent).toContain(en.identity.sessionHint);
 const dialog=document.querySelector<HTMLElement>('[role="dialog"]')!;
 [...dialog.querySelectorAll('button')].find(b=>b.textContent?.trim()===en.securitySettings.confirm)!.click();
 await expect.poll(()=>put.mock.calls.length).toBe(1);
 expect(put.mock.calls[0]).toEqual(['/v1/settings/security',expect.objectContaining({password_min_length:24})]);
 assertFits(container);
});

test('company review enforcement stays visible and cannot be disabled personally',async()=>{
 mockSecurity();
 vi.mocked(api.get).mockImplementation(async path=>path==='/v1/users/me/mcp-preferences'?{review_required:false,enforced:true,effective_review_required:true} as never:{items:[]} as never);
 const {container}=await render(AgentReviewPreferences);
 await expect.poll(()=>container.querySelector('[role="switch"]')?.getAttribute('aria-checked')).toBe('true');
 expect(container.querySelector<HTMLButtonElement>('[role="switch"]')!.disabled).toBe(true);
 expect(container.textContent).toContain(en.agentReview.enforced);
 expect(container.textContent).not.toContain(en.agentReview.preferencesHint);
 expect(container.firstElementChild!.getBoundingClientRect().width).toBeLessThanOrEqual(672);
});

test.each(['light','dark'])('permission editor applies server presets within the returned grant ceiling in %s',async theme=>{
 document.documentElement.classList.toggle('dark',theme==='dark');
 const grants:PermissionGrant={'event_types.links':'own','team_event_types.links':'team','team_event_types.custom_times':'team'};
 vi.spyOn(api,'get').mockResolvedValue({items:[
  {id:'event_types.links',group:'event_types',scopes:['own','all'],delegable:true},
  {id:'team_event_types.links',group:'team_event_types',scopes:['team','all'],delegable:true},
  {id:'team_event_types.custom_times',group:'team_event_types',scopes:['team','all'],delegable:true},
  {id:'settings.update',group:'settings',scopes:['all'],delegable:true}
 ],permissions:grants,delegable:grants,presets:{assistant:{...grants,'settings.update':'all'}}});
 const {container}=await render(PermissionEditor,{props:{value:{},delegated:true}});
 await expect.poll(()=>container.querySelectorAll('fieldset').length).toBe(2);
 expect(container.textContent).toContain('Personal event types');
 expect(container.textContent).not.toContain(en.permissions.groups.settings);
 container.querySelector<HTMLButtonElement>('[aria-label="'+en.permissions.startFrom+'"]')!.click();
 await expect.poll(()=>[...document.querySelectorAll<HTMLElement>('[role="option"]')].some(o=>o.textContent?.trim()===en.permissions.presets.assistant)).toBe(true);
 await page.getByRole('option',{name:en.permissions.presets.assistant,exact:true}).click();
 await expect.poll(()=>container.querySelector('[aria-labelledby="permission-team_event_types.links"]')?.textContent).toContain(en.permissions.scope.team);
 expect(container.querySelector('[aria-labelledby="permission-event_types.links"]')?.textContent).toContain(en.permissions.scope.own);
 container.querySelector<HTMLButtonElement>('[aria-labelledby="permission-event_types.links"]')!.click();
 await expect.poll(()=>document.querySelector('[role="listbox"]')).not.toBeNull();
 const options=[...document.querySelectorAll('[role="listbox"] [role="option"]')].map(o=>o.textContent?.trim());
 expect(options).not.toContain(en.permissions.scope.team);
 expect(options).not.toContain(en.permissions.scope.all);
 assertFits(container);
});
