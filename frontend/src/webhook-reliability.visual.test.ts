import {afterEach,expect,test,vi} from 'vitest';
import {cleanup,render} from 'vitest-browser-svelte';
import {page} from 'vitest/browser';
import {api,type User} from '$lib/api';
import {currentUser} from '$lib/stores';
import WebhooksManager from '$lib/components/WebhooksManager.svelte';
import TeamsManager from '$lib/components/TeamsManager.svelte';
import en from '$lib/i18n/en.json';
afterEach(async()=>{cleanup();vi.restoreAllMocks();currentUser.set(null);document.documentElement.classList.remove('dark');await page.viewport(1280,720)});

test.each(['light','dark'])('webhook editor preserves scopes and edits all settings on mobile in %s',async theme=>{
 await page.viewport(390,844);document.documentElement.classList.toggle('dark',theme==='dark');
 currentUser.set({id:'me',permissions:{'webhooks.read':'own','webhooks.update':'own','bookings.read':'own'}} as unknown as User);
 const hook={id:'hook',url:'https://automation.example.test/old',events:['booking.created'],fields:['id'],scopes:[{type:'user',id:'me'}],is_active:true,created_at:'2026-11-01T00:00:00Z'};
 vi.spyOn(api,'get').mockImplementation(async path=>path==='/v1/webhooks'?{items:[hook]}:path==='/v1/webhooks/scopes'?{items:[{type:'user',id:'me',name:'Me'}]}:{items:[]} as never);
 const patch=vi.spyOn(api,'patch').mockResolvedValue(undefined);
 await render(WebhooksManager);await page.getByRole('button',{name:en.webhookScopes.edit,exact:true}).click();
 const dialog=page.getByRole('dialog');await dialog.getByRole('textbox',{name:en.webhooks.new.urlLabel}).fill('https://automation.example.test/new');
 await dialog.getByRole('checkbox',{name:'booking.cancelled',exact:true}).click();
 await dialog.getByRole('checkbox',{name:en.common.active,exact:true}).click();
 await page.screenshot({path:`./__screenshots__/webhook-editor-${theme}.png`});
 expect(document.documentElement.scrollWidth).toBeLessThanOrEqual(390);
 await dialog.getByRole('button',{name:en.common.save,exact:true}).click();
 await expect.poll(()=>patch.mock.calls.length).toBe(1);
 expect(patch.mock.calls[0]).toEqual(['/v1/webhooks/hook',{url:'https://automation.example.test/new',events:['booking.created','booking.cancelled'],fields:['id'],scopes:[{type:'user',id:'me'}],is_active:false}]);
});

test('new webhooks require explicit opt-in to disclose a booking token',async()=>{
 currentUser.set({id:'me',is_admin:true} as User);
 vi.spyOn(api,'get').mockResolvedValue({items:[]});
 const {container}=await render(WebhooksManager);
 await page.getByRole('button',{name:en.webhooks.new.toggleOpen,exact:true}).click();
 const token=page.getByRole('checkbox',{name:en.webhooks.lifecycleFields.token,exact:true});
 await expect.element(token).not.toBeChecked();
 await token.click();await expect.element(token).toBeChecked();
 await expect.element(page.getByRole('dialog')).toHaveTextContent(en.webhooks.lifecycleFields.linkId);
});
test.each(['light','dark'])('failed webhook retry is reviewed and preserves its delivery target in %s',async theme=>{
 await page.viewport(390,844);document.documentElement.classList.toggle('dark',theme==='dark');
 currentUser.set({id:'me',permissions:{'webhooks.read':'own','webhooks.update':'own','webhooks.deliveries':'own'}} as unknown as User);
 vi.spyOn(api,'get').mockImplementation(async path=>path==='/v1/webhooks'?{items:[{id:'hook',url:'https://automation.example.test/webhook/booking-events',events:['booking.created'],fields:[],is_active:true,created_at:'2026-11-01T00:00:00Z'}]}:path==='/v1/webhooks/hook/deliveries'?{items:[{id:'delivery',status:'failed',event:'booking.created',attempt_count:24,last_error:'webhook endpoint returned HTTP 503',last_attempted_at:'2026-11-21T10:00:00Z'}]}:{items:[]} as never);
 const post=vi.spyOn(api,'post').mockResolvedValue({status:'pending'});
 const {container}=await render(WebhooksManager);
 await page.getByRole('button',{name:en.webhooks.tooltip.deliveries,exact:true}).click();
 await page.screenshot({path:`./__screenshots__/webhook-retry-${theme}.png`});
 await page.getByRole('button',{name:en.webhooks.retry,exact:true}).click();
 await expect.poll(()=>document.querySelector('[role="alertdialog"]')?.textContent).toContain(en.webhooks.retryHint);
 expect(post).not.toHaveBeenCalled();
 await page.getByRole('alertdialog').getByRole('button',{name:en.webhooks.retry,exact:true}).click();
 await expect.poll(()=>post.mock.calls.length).toBe(1);expect(post.mock.calls[0]).toEqual(['/v1/webhooks/hook/deliveries/delivery/retry',{}]);
 expect(container.scrollWidth).toBeLessThanOrEqual(390);
});
test('team routing priority saves independently from membership role',async()=>{
 await page.viewport(390,844);
 currentUser.set({id:'owner',is_admin:true,permissions:{'teams.read':'all','teams.members':'all','teams.update':'all'}} as unknown as User);
 const team={id:'ops',name:'Operations',slug:'ops',member_count:1,members:[{id:'sam',name:'Sam Taylor',email:'sam@example.test',role:'member',routing_priority:7}]};
 vi.spyOn(api,'get').mockImplementation(async path=>path==='/v1/teams'?{items:[team]}:path==='/v1/teams/ops'?team:{items:[]} as never);
 const patch=vi.spyOn(api,'patch').mockResolvedValue(team);
 const {container}=await render(TeamsManager);await page.getByRole('button',{name:'Manage',exact:true}).click();
 await expect.poll(()=>container.querySelector('input[type="number"]')).not.toBeNull();
 expect(container.scrollWidth).toBeLessThanOrEqual(390);
 await page.screenshot({path:'./__screenshots__/team-routing-priority-mobile.png'});
 const input=page.getByRole('spinbutton',{name:'Routing priority for Sam Taylor',exact:true});await input.fill('2');await page.getByRole('heading',{name:'Teams',exact:true}).click();
 await expect.poll(()=>patch.mock.calls.length).toBe(1);expect(patch.mock.calls[0]).toEqual(['/v1/teams/ops/members/sam',{routing_priority:2}]);await expect.poll(()=>vi.mocked(api.get).mock.calls.filter(call=>call[0]==='/v1/teams/ops').length).toBe(2);
});
