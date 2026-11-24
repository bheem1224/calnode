import {afterEach,expect,test,vi} from 'vitest';
import {cleanup,render} from 'vitest-browser-svelte';
import {page} from 'vitest/browser';
import {api} from '$lib/api';
import Operations from '$lib/components/SecurityOperations.svelte';
import en from '$lib/i18n/en.json';

afterEach(async()=>{cleanup();vi.restoreAllMocks();document.documentElement.classList.remove('dark');await page.viewport(1280,720)});

test.each(['light','dark'])('retained provider recovery is visible and readable on mobile in %s',async theme=>{
 await page.viewport(320,800);document.documentElement.classList.toggle('dark',theme==='dark');
 vi.spyOn(api,'get').mockResolvedValue({failed_jobs:[],calendar_errors:[],failed_confirmations:1,pending_jobs:2,provider_work:[
  {id:'calendar-cleanup:seat:one',type:'calendar.cleanup',owner:'Calendar owner',booking_id:'9e5549ed-a71e-45b9-85db-a35afcbb648d',error:'Calendar cleanup pending; check provider access'},
  {id:'zoom-recovery:booking:one',type:'zoom.reconcile',owner:'Meeting host',booking_id:'abb48764-1782-4193-a6ca-f792283a3d9e',error:'Creation outcome unresolved. Reconciliation will not create another meeting. Check Zoom; cancel and rebook if recovery remains unresolved.'}
 ]});
 const post=vi.spyOn(api,'post').mockResolvedValue({ok:true});
 const {container}=await render(Operations);
 await expect.element(page.getByText(en.securityOperations.zoomRecovery,{exact:true})).toBeVisible();
 await expect.element(page.getByText(en.securityOperations.healthy,{exact:true})).not.toBeInTheDocument();
 expect(document.documentElement.scrollWidth).toBeLessThanOrEqual(320);
 const buttons=[...container.querySelectorAll('button')].filter(b=>b.textContent===en.securityOperations.retry);
 expect(buttons).toHaveLength(2);
 for(const button of buttons)expect(button.getBoundingClientRect().height).toBeGreaterThanOrEqual(44);
 await page.getByRole('region',{name:en.securityOperations.title,exact:true}).screenshot({path:`./__screenshots__/provider-recovery-${theme}.png`});
 buttons[1].click();
 await expect.element(page.getByRole('dialog')).toBeVisible();
 expect(post).not.toHaveBeenCalled();
 await page.getByRole('dialog').getByRole('button',{name:en.common.cancel,exact:true}).click();
 expect(post).not.toHaveBeenCalled();
});
