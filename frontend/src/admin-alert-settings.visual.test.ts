import {afterEach,expect,test,vi} from 'vitest';
import {cleanup,render} from 'vitest-browser-svelte';
import {page} from 'vitest/browser';
import {api} from '$lib/api';
import SecuritySettings from './routes/settings/security/+page.svelte';
import en from '$lib/i18n/en.json';

afterEach(async()=>{cleanup();vi.restoreAllMocks();document.documentElement.classList.remove('dark');await page.viewport(1280,720)});

test.each(['light','dark'])('administrator alert recipients save with review and fit mobile in %s',async theme=>{
 await page.viewport(390,844);document.documentElement.classList.toggle('dark',theme==='dark');
 const policy={admin_alert_mode:'owner',admin_alert_user_ids:[] as string[],password_min_length:16,mcp_review_enforced:false,require_admin_mfa:false,require_member_mfa:false,session_hours:12,dynamic_link_days:7,members_sso_only:false,admin_password_enabled:true};
 const recipients=[{id:'owner',name:'Owner',email:'owner@example.test',is_owner:true},{id:'it',name:'IT operations team',email:'calendar-and-security-notifications@example.test',is_owner:false}];
 vi.spyOn(api,'get').mockImplementation(async path=>path==='/v1/settings/security'?{policy:{...policy},alert_recipients:recipients}:path==='/v1/settings/security/operations'?{failed_jobs:[],calendar_errors:[],failed_confirmations:0,pending_jobs:0}:{items:[]} as never);
 const put=vi.spyOn(api,'put').mockImplementation(async(_path,body)=>({policy:body,alert_recipients:recipients}));
 const {container}=await render(SecuritySettings);
 await expect.poll(()=>container.querySelector('#admin-alert-mode')?.textContent).toContain(en.adminAlerts.owner);
 container.querySelector<HTMLButtonElement>('#admin-alert-mode')!.click();
 await page.getByRole('option',{name:en.adminAlerts.selected,exact:true}).click();
 await expect.element(page.getByRole('button',{name:en.securitySettings.save,exact:true})).toBeDisabled();
 await page.getByRole('checkbox',{name:'IT operations team (calendar-and-security-notifications@example.test)',exact:true}).click();
 await expect.element(page.getByRole('button',{name:en.securitySettings.save,exact:true})).not.toBeDisabled();
 container.querySelector('#admin-alerts-title')!.scrollIntoView({block:'start'});
 await page.getByRole('region',{name:en.adminAlerts.title,exact:true}).screenshot({path:`./__screenshots__/admin-alerts-${theme}.png`});
 expect(document.documentElement.scrollWidth).toBeLessThanOrEqual(390);
 await page.getByRole('button',{name:en.securitySettings.save,exact:true}).click();
 expect(put).not.toHaveBeenCalled();
 await page.getByRole('dialog').getByRole('button',{name:en.securitySettings.confirm,exact:true}).click();
 await expect.poll(()=>put.mock.calls.length).toBe(1);
 expect(put.mock.calls[0]).toEqual(['/v1/settings/security',expect.objectContaining({admin_alert_mode:'selected',admin_alert_user_ids:['it']})]);
});
