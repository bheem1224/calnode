// Run only against a freshly started, disposable localhost instance.
// CALNODE_IDENTITY_TEST_URL=http://localhost:39140 node scripts/verify-identity-providers.mjs
import { chromium } from 'playwright';
import { randomBytes } from 'node:crypto';
import assert from 'node:assert/strict';

const origin=process.env.CALNODE_IDENTITY_TEST_URL;
if(!origin||new URL(origin).hostname!=='localhost')throw new Error('A fresh localhost instance is required');
const browser=await chromium.launch({headless:true});
try{
 const context=await browser.newContext({viewport:{width:1440,height:900}});
 const status=await context.request.get(origin+'/v1/auth/status');
 assert.equal((await status.json()).claimed,false,'Refusing to change an existing instance');
 const password=randomBytes(24).toString('base64url');
 const claim=await context.request.post(origin+'/v1/auth/claim',{headers:{Origin:origin},data:{name:'Provider Test',email:'owner@example.test',password,timezone:'UTC'}});assert.ok(claim.ok());
 const page=await context.newPage();const errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.goto(origin+'/admin/settings/identity');
 const dialog=page.getByRole('dialog');
 async function create(name,purpose){
  await page.getByRole('button',{name:'+ Add provider',exact:true}).click();
  if(purpose!=='SSO only'){await page.getByLabel('Use for',{exact:true}).click();await page.getByRole('option',{name:purpose,exact:true}).click();}
  await page.getByLabel('Display name',{exact:true}).fill(name);
  await page.getByLabel('Client ID',{exact:true}).fill('fake-'+randomBytes(8).toString('hex'));
  await page.getByLabel('Client secret',{exact:true}).fill('disposable-test-secret');
  if(purpose==='SSO only'){
   await page.getByLabel('Microsoft tenant ID',{exact:true}).fill('11111111-1111-4111-8111-111111111111');
   await page.getByRole('switch',{name:'Automatically enroll eligible Members',exact:true}).click();
   await page.getByLabel('Approved email domains',{exact:true}).fill('example.test');await page.getByLabel('Approved email domains',{exact:true}).press('Enter');
   await page.getByLabel('Microsoft security group object IDs',{exact:true}).fill('11111111-1111-4111-8111-111111111111,22222222-2222-4222-8222-222222222222');await page.getByLabel('Microsoft security group object IDs',{exact:true}).press('Enter');
   await page.getByLabel('Group membership rule',{exact:true}).click();await page.getByRole('option',{name:'All listed groups',exact:true}).click();
  }
  await dialog.getByRole('button',{name:'Review changes',exact:true}).click();
  await page.getByLabel('Current password',{exact:true}).fill(password);
  const verified=page.waitForResponse(r=>r.url().endsWith('/v1/auth/reauthenticate'));
  await dialog.getByRole('button',{name:'Verify',exact:true}).click();assert.ok((await verified).ok());
  const saved=page.waitForResponse(r=>r.url().endsWith('/v1/settings/identity-providers')&&r.request().method()==='POST');
  await dialog.getByRole('button',{name:'Save',exact:true}).click();const response=await saved;assert.ok(response.ok(),await response.text());const provider=await response.json();
  assert.equal(provider.client_secret,undefined);
  if(purpose==='SSO only'){await dialog.getByText(origin+'/v1/auth/providers/'+provider.id+'/callback',{exact:true}).waitFor();assert.equal(provider.group_match,'all');assert.equal(provider.group_ids.length,2);}
  else{assert.equal(provider.microsoft_tenant_id,'common');}
  await dialog.getByRole('button',{name:'Close',exact:true}).click();return provider;
 }
 const company=await create('Company SSO','SSO only');const personal=await create('Personal calendars','Calendar & conferencing only');
 const publicStatus=await context.request.get(origin+'/v1/auth/status');const providers=(await publicStatus.json()).identity_providers;
 assert.deepEqual(providers.map(p=>p.id),[company.id]);
 const calendar=await context.request.get(origin+'/v1/calendar/status');assert.deepEqual((await calendar.json()).integration_providers.map(p=>p.id),[personal.id]);
 const deny=await context.request.get(origin+'/v1/auth/providers/'+personal.id+'/login',{maxRedirects:0});assert.equal(deny.status(),404);
 for(const route of ['google','microsoft']){await page.goto(origin+'/admin/settings/'+route);await page.waitForURL(origin+'/admin/settings/identity');}
 await page.goto(origin+'/admin/settings/security');await page.getByText('Require SSO sign-in for Members',{exact:true}).waitFor();
 assert.equal(await page.getByLabel('Microsoft security group object ID',{exact:true}).count(),0);
 assert.deepEqual(errors,[]);
 console.log('PASS: provider setup, multiple groups, review/reauthentication, callback URLs, calendar isolation, legacy redirects, generic SSO policy');
}finally{await browser.close();}
