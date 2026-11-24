// Uses the disposable, authenticated browser context from verify-agent-operations.
import assert from 'node:assert/strict';
import { join } from 'node:path';
import { mkdir } from 'node:fs/promises';

export async function verifyNotificationSettings(context,page,origin,screenshots){
 const mutate=async(method,path,data)=>{const response=await context.request.fetch(origin+path,{method,headers:{Origin:origin},data});assert.ok(response.ok(),await response.text());return response.json();};
 const library=page.locator('section').filter({has:page.getByRole('heading',{name:'Saved email templates',exact:true})});
 await page.goto(origin+'/admin/settings/notifications');await library.waitFor();
 await library.getByRole('button',{name:'+ New template',exact:true}).click();
 await page.getByRole('button',{name:'Booking confirmation',exact:true}).click();
 let modal=page.getByRole('dialog');await modal.getByPlaceholder('Template name (e.g. Founder Intro)').fill('Automation confirmation');
 await modal.getByRole('tab',{name:'Custom HTML',exact:true}).click();
 await modal.locator('textarea').fill('<p>Original library copy for {Event}</p>');
 await modal.getByText('Set as default for new event types',{exact:true}).click();
 await modal.getByRole('button',{name:'Save template',exact:true}).click();await modal.waitFor({state:'hidden'});
 await library.getByRole('button',{name:'Edit',exact:true}).click();modal=page.getByRole('dialog');
 assert.equal(await modal.getByRole('tab',{name:'Custom HTML',exact:true}).getAttribute('aria-selected'),'true');
 await modal.locator('textarea').fill('<p>Updated library copy for {Event}</p><script>window.untrusted=true</script>');
 await modal.getByRole('button',{name:'Save template',exact:true}).click();await modal.waitFor({state:'hidden'});
 const saved=(await(await context.request.get(origin+'/v1/email-templates')).json()).items.find(t=>t.name==='Automation confirmation');
 assert.ok(saved?.is_default);assert.equal(saved.mode,'html');assert.ok(saved.body.includes('Updated library copy'));assert.ok(!saved.body.includes('<script'));
 assert.equal(await page.evaluate(()=>window.untrusted),undefined);
 const event=await mutate('POST','/v1/event-types',{slug:'notification-library-event',name:'Library event',duration_minutes:30,location_type:'in_person',location_value:'Office'});
 const inherited=await(await context.request.get(origin+'/v1/event-types/notification-library-event')).json();assert.ok(inherited.html_confirmation.includes('Updated library copy'));
 async function capture(name){
  if(!screenshots)return;await mkdir(screenshots,{recursive:true});
  for(const mode of ['light','dark']){
   await page.evaluate(theme=>localStorage.setItem('fp-theme',theme),mode);await page.reload();
   await page.getByRole('heading',{name:name==='notifications'?'Saved email templates':'Webhooks',exact:true}).waitFor();
   await page.waitForFunction(expected=>getComputedStyle(document.body).color===expected,mode==='dark'?'rgb(244, 244, 245)':'rgb(17, 24, 39)');
   assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
   await page.screenshot({path:join(screenshots,`${name}-${mode}.png`)});
  }
 }
 await capture('notifications');
 const team=await mutate('POST','/v1/teams',{name:'Browser notification team',slug:'browser-notifications'});
 await page.goto(origin+'/admin/webhooks');await page.getByRole('button',{name:'New webhook',exact:true}).click();
 await page.getByLabel('Endpoint URL',{exact:true}).fill('https://93.184.216.34/automation');
 await page.getByRole('button',{name:'Select webhook scopes',exact:true}).click();
 await page.getByText('All team meetings: Browser notification team',{exact:true}).click();
 await page.getByText('Event type: Library event',{exact:true}).click();
 await page.keyboard.press('Escape');
 const creation=page.waitForResponse(r=>r.url()===origin+'/v1/webhooks'&&r.request().method()==='POST');
 await page.getByRole('button',{name:'Create webhook',exact:true}).click();const made=await creation;assert.equal(made.status(),201);const hook=await made.json();
 assert.equal(hook.scopes.length,3);assert.ok(hook.scopes.some(s=>s.type==='team'&&s.id===team.id));assert.ok(hook.scopes.some(s=>s.type==='event'&&s.id===event.id));
 await page.getByText(hook.secret,{exact:true}).waitFor();
 await page.getByRole('button',{name:'Edit webhook',exact:true}).click();
 modal=page.getByRole('dialog');
 assert.equal(await modal.getByLabel('Endpoint URL',{exact:true}).inputValue(),hook.url);
 await modal.getByLabel('Endpoint URL',{exact:true}).fill('https://93.184.216.34/updated-automation');
 await modal.getByRole('button',{name:'Select webhook scopes',exact:true}).click();await page.getByText('All my meetings',{exact:true}).last().click();await page.keyboard.press('Escape');
 const cancelled=modal.getByRole('checkbox',{name:'booking.cancelled',exact:true});
 const wasCancelled=await cancelled.isChecked();await cancelled.click();
 await modal.getByRole('checkbox',{name:'Active',exact:true}).uncheck();
 const editing=page.waitForResponse(r=>r.url()===origin+'/v1/webhooks/'+hook.id&&r.request().method()==='PATCH');
 await modal.getByRole('button',{name:'Save',exact:true}).click();
 const edited=await editing;assert.equal(edited.status(),204);assert.equal(JSON.parse(edited.request().postData()).secret,undefined);
 await modal.waitFor({state:'hidden'});
 const updated=(await(await context.request.get(origin+'/v1/webhooks')).json()).items.find(w=>w.id===hook.id);
 assert.equal(updated.scopes.length,2);assert.equal(updated.url,'https://93.184.216.34/updated-automation');assert.equal(updated.is_active,false);
 assert.equal(updated.events.includes('booking.cancelled'),!wasCancelled);
 await page.getByRole('button',{name:'Edit webhook',exact:true}).click();modal=page.getByRole('dialog');
 assert.equal(await modal.getByLabel('Endpoint URL',{exact:true}).inputValue(),updated.url);
 assert.equal(await modal.getByRole('checkbox',{name:'Active',exact:true}).isChecked(),false);
 assert.equal(await modal.getByRole('checkbox',{name:'booking.cancelled',exact:true}).isChecked(),!wasCancelled);
 await modal.getByRole('checkbox',{name:'Active',exact:true}).check();
 await modal.getByRole('button',{name:'Save',exact:true}).click();await modal.waitFor({state:'hidden'});
 await capture('webhooks');
 await page.goto(origin+'/admin/settings/notifications');await library.getByRole('button',{name:'Delete',exact:true}).click();await page.getByRole('alertdialog').getByRole('button',{name:'Confirm',exact:true}).click();
 await page.getByText('No matching templates.',{exact:true}).waitFor();
 console.log('PASS: notification template create/edit/default/delete, shared sanitized builder and webhook URL/scopes/events/active edit persistence');
}
