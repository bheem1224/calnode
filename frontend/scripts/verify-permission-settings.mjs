// Called by the fresh-instance agent verification. Uses real invitations and
// separate browser sessions; never accepts a production host or existing account.
import assert from 'node:assert/strict';
import { randomBytes } from 'node:crypto';
import { mkdir } from 'node:fs/promises';
import { join } from 'node:path';

export async function verifyPermissionSettings(owner, page, origin, screenshots) {
 assert.equal(new URL(origin).hostname,'localhost');
 const post=async(path,data,context=owner)=>{
  const response=await context.request.post(origin+path,{data,headers:{Origin:origin}});
  assert.ok(response.ok(),`${path}: ${response.status()} ${await response.text()}`);
  return response.json();
 };
 const grants={'team_event_types.read':'team','team_event_types.links':'team','team_event_types.custom_times':'team','api_keys.read':'own','api_keys.create':'own','mcp.connect':'own'};
 const role=await post('/v1/roles',{name:'Scoped team scheduling',permissions:grants});
 const team=await post('/v1/teams',{name:'Permission verification team',slug:'permission-verification-team'});
 const event=await post('/v1/event-types',{slug:'permission-team-event',name:'Permission scoped event',team_id:team.id,duration_minutes:30,location_type:'phone',location_value:'+15551234567'});
 const email='permission-member@example.test';
 const invite=await post('/v1/invites',{email,role:'member',custom_role_id:role.id});
 const token=new URL(invite.invite_url).pathname.split('/').at(-1);
 const member=await owner.browser().newContext({viewport:{width:1440,height:900},reducedMotion:'reduce'});
 const memberPage=await member.newPage();const errors=[];memberPage.on('pageerror',e=>errors.push(e.message));
 try {
  const account=await post('/v1/invites/'+encodeURIComponent(token)+'/claim',{name:'Scoped Member',password:randomBytes(24).toString('base64url'),timezone:'UTC'},member);
  await post('/v1/teams/'+team.id+'/members',{user_id:account.user_id,role:'member'});
  const me=await (await member.request.get(origin+'/v1/users/me')).json();
  assert.equal(me.permissions['event_types.read'],undefined);
  assert.equal(me.permissions['team_event_types.update'],undefined);
  await memberPage.goto(origin+'/admin');
  await memberPage.locator('nav').getByRole('link',{name:'Event Types',exact:true}).first().click();
  await memberPage.getByText('Permission scoped event',{exact:true}).waitFor();
  const list=await (await member.request.get(origin+'/v1/event-types?scope=all')).json();
  assert.deepEqual(list.items.map(e=>e.id),[event.id]);
  assert.equal(list.items[0].can_edit,false);
  assert.equal(list.items[0].can_generate_links,true);
  assert.equal(list.items[0].can_offer_custom_times,true);
  const denied=await member.request.patch(origin+'/v1/event-types/'+event.slug,{data:{name:'Forbidden'},headers:{Origin:origin}});
  assert.ok([403,404].includes(denied.status()));
  await post('/v1/event-types/'+event.id+'/single-use-links',{},member);
  await memberPage.goto(origin+'/admin/api-keys');
  await memberPage.getByRole('button',{name:'New key',exact:true}).click();
  await memberPage.getByLabel("Start from a template",{exact:true}).click();
  await memberPage.getByRole('option',{name:'Scheduling assistant',exact:true}).click();
  assert.equal(await memberPage.getByText('Personal event types',{exact:true}).count(),0);
  const group=memberPage.locator('fieldset').filter({has:memberPage.locator('legend').getByText('Team event types',{exact:true})});
  await group.getByRole('combobox',{name:'Generate one-time links',exact:true}).click();
  assert.equal(await memberPage.getByRole('option',{name:'Own resources',exact:true}).count(),0);
  assert.equal(await memberPage.getByRole('option',{name:'Entire instance',exact:true}).count(),0);
  await memberPage.keyboard.press('Escape');
  if(screenshots){await mkdir(screenshots,{recursive:true});for(const theme of ['light','dark']){
   await memberPage.evaluate(theme=>{localStorage.setItem('fp-theme',theme);document.documentElement.classList.toggle('dark',theme==='dark');},theme);
   assert.ok(await memberPage.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
   await memberPage.screenshot({path:join(screenshots,`team-key-permissions-${theme}.png`),animations:'disabled'});
  }}
  // The search-permissions field also has a placeholder, so use its form label.
  await memberPage.getByLabel('Key name',{exact:true}).fill('Scoped scheduling key');
  const createdResponse=memberPage.waitForResponse(r=>r.url().endsWith('/v1/api-keys')&&r.request().method()==='POST');
  await memberPage.getByRole('button',{name:'Create key',exact:true}).click();
  const created=await createdResponse;assert.ok(created.ok(),await created.text());
  const key=await created.json();
  const inventory=await (await member.request.get(origin+'/v1/api-keys')).json();
  const saved=inventory.items.find(item=>item.id===key.id);assert.ok(saved);
  assert.equal(saved.permissions['team_event_types.links'],'team');
  assert.equal(saved.permissions['team_event_types.custom_times'],'team');
  assert.equal(saved.permissions['team_event_types.update'],undefined);
  assert.equal(saved.permissions['event_types.update'],undefined);
  assert.ok((await member.request.get(origin+'/v1/event-types?scope=all',{headers:{'X-API-Key':key.key}})).ok());
  assert.deepEqual(errors,[]);
 } finally {await member.close();}
 // Long explanations are available by keyboard help; consequential session
 // effects remain visible when the owner reviews a security-policy change.
 for(const route of ['account-security','security']){
  await page.goto(origin+'/admin/settings/'+route);
  await page.getByRole('heading',{name:route==='security'?'Security & sign-in':'Account security',exact:true}).waitFor();
  const help=page.getByRole('button',{name:route==='security'?'Help: Password policy':'Help: Passkeys · recommended',exact:true});
  await page.keyboard.press('Tab');await help.focus();await page.getByRole('tooltip').waitFor();await page.keyboard.press('Escape');
  if(screenshots)for(const theme of ['light','dark']){
   await page.evaluate(theme=>{localStorage.setItem('fp-theme',theme);document.documentElement.classList.toggle('dark',theme==='dark');},theme);
   assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
   await page.screenshot({path:join(screenshots,`${route}-compact-${theme}.png`),fullPage:true,animations:'disabled'});
  }
 }
 console.log('PASS: team-only navigation, real scoped role/key preset persistence, denied configuration, one-time links and compact security help');
}
