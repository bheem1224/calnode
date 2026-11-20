// Run against a fresh disposable localhost instance with the current frontend.
import { chromium } from 'playwright';
import { randomBytes,createHash } from 'node:crypto';
import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';
import { join } from 'node:path';
const origin=process.env.CALNODE_AGENT_TEST_URL;
if(!origin||new URL(origin).hostname!=='localhost')throw Error('A fresh disposable localhost instance is required');
const browser=await chromium.launch({headless:true});
try{
 const context=await browser.newContext({viewport:{width:1440,height:1000}});
 const status=await context.request.get(origin+'/v1/auth/status');assert.equal((await status.json()).claimed,false,'Refusing an existing instance');
 const password=randomBytes(24).toString('base64url');
 const claim=await context.request.post(origin+'/v1/auth/claim',{headers:{Origin:origin},data:{name:'Agent Test Owner',email:'agent-owner@example.test',password,timezone:'UTC'}});assert.ok(claim.ok(),await claim.text());
 const page=await context.newPage();const errors=[];page.on('pageerror',e=>errors.push(e.message));
 const redirect=origin+'/test-agent-callback';
 const registration=await context.request.post(origin+'/oauth/register',{data:{client_name:'Agent verification',redirect_uris:[redirect]}});assert.ok(registration.ok());const {client_id}=await registration.json();
 const verifier=randomBytes(32).toString('base64url');const challenge=createHash('sha256').update(verifier).digest('base64url');
 await page.route('**/test-agent-callback?**',route=>route.fulfill({status:200,contentType:'text/html',body:'Authorization captured for test'}));
 const params=new URLSearchParams({client_id,redirect_uri:redirect,response_type:'code',code_challenge:challenge,code_challenge_method:'S256',scope:'mcp',state:'agent-test'});
 await page.goto(origin+'/oauth/authorize?'+params);
 await page.getByLabel('Start with a profile').selectOption('scheduling');
 assert.equal(await page.locator('input[name="permissions"][value="event_types.links"]').isChecked(),true);
 assert.equal(await page.locator('input[name="permissions"][value="team_event_types.custom_times"]').isChecked(),true);
 assert.equal(await page.locator('input[name="permissions"][value="event_types.update"]').isChecked(),false);
 await page.getByLabel('Start with a profile').selectOption('workspace_operator');
 assert.equal(await page.locator('input[name="permissions"][value="logs.read"]').isChecked(),true);
 assert.equal(await page.locator('input[name="permissions"][value="transcripts.read"]').isChecked(),false);
 await page.getByRole('button',{name:'Allow',exact:true}).click();await page.waitForURL('**/test-agent-callback?**');
 const code=new URL(page.url()).searchParams.get('code');assert.ok(code);
 const tokenResponse=await context.request.post(origin+'/oauth/token',{form:{grant_type:'authorization_code',code,client_id,redirect_uri:redirect,code_verifier:verifier}});assert.ok(tokenResponse.ok(),await tokenResponse.text());const {access_token}=await tokenResponse.json();
 let seq=0;
 async function rpc(method,params){const response=await context.request.post(origin+'/mcp',{headers:{Authorization:'Bearer '+access_token,Accept:'application/json, text/event-stream','MCP-Protocol-Version':'2025-03-26'},data:{jsonrpc:'2.0',id:++seq,method,params}});const body=await response.text();assert.ok(response.ok(),body);const payload=body.split('\n').find(line=>line.startsWith('data:'))?.slice(5).trim()??body;const json=JSON.parse(payload);assert.equal(json.error,undefined,JSON.stringify(json.error));return json.result;}
 await rpc('initialize',{protocolVersion:'2025-03-26',capabilities:{},clientInfo:{name:'agent-verification',version:'1'}});
 const tools=await rpc('tools/list',{});assert.ok(tools.tools.some(tool=>tool.name==='create_event_type'));assert.ok(tools.tools.find(tool=>tool.name==='cancel_booking').annotations.destructiveHint);
 const call=await rpc('tools/call',{name:'import_event_type',arguments:{values:{buffer_before_minutes:10,allow_multiple_durations:true,duration_options:[15,30],requires_confirmation:true,reminders:[24,1],slug:'agent-import',name:'Agent imported event',duration_minutes:30,location_type:'phone',location_value:'+1 555 123 4567'}}});assert.ok(!call.isError,JSON.stringify(call.content));const proposal=call.structuredContent;assert.equal(proposal.status,'pending');assert.ok(proposal.review_url);
 const before=await context.request.get(origin+'/v1/event-types');assert.ok(!(await before.json()).items.some(item=>item.slug==='agent-import'));
 await page.goto(proposal.review_url);await page.getByRole('heading',{name:'Review agent changes',exact:true}).waitFor();await page.getByRole('button',{name:'Approve these changes',exact:true}).click();const confirm=page.getByRole('alertdialog');await confirm.getByRole('button',{name:'Confirm',exact:true}).click();await page.getByText('Completed',{exact:true}).waitFor();
 const after=await context.request.get(origin+'/v1/event-types/agent-import');assert.ok(after.ok());const imported=await after.json();assert.equal(imported.is_active,false);assert.equal(imported.is_public,false);assert.equal(imported.buffer_before_minutes,10);assert.equal(imported.allow_multiple_durations,true);assert.deepEqual(imported.duration_options,[15,30]);assert.deepEqual(imported.reminders,[24,1]);
 const replay=await context.request.post(origin+'/v1/mcp/change-requests/'+proposal.id+'/decision',{headers:{Origin:origin},data:{approve:true}});assert.equal(replay.status(),409);
 await page.goto(origin+'/admin/settings/account-security');await page.getByText('Agent review preferences',{exact:true}).waitFor();await page.goto(origin+'/admin/connections');await page.getByText('Agent review preferences',{exact:true}).waitFor();
 if(process.env.CALNODE_SCREENSHOT_DIR){await mkdir(process.env.CALNODE_SCREENSHOT_DIR,{recursive:true});for(const mode of ['light','dark']){await page.evaluate(theme=>localStorage.setItem('fp-theme',theme),mode);await page.reload();await page.getByText(new URL(page.url()).pathname.endsWith('/review')?'Completed':'Agent review preferences',{exact:true}).waitFor();await page.waitForFunction(expected=>getComputedStyle(document.body).color===expected,mode==='dark'?'rgb(244, 244, 245)':'rgb(17, 24, 39)');await page.screenshot({path:join(process.env.CALNODE_SCREENSHOT_DIR,`agent-preferences-${mode}.png`)});await page.goto(proposal.review_url);await page.getByText('Completed',{exact:true}).waitFor();await page.evaluate(theme=>localStorage.setItem('fp-theme',theme),mode);await page.reload();await page.getByText(new URL(page.url()).pathname.endsWith('/review')?'Completed':'Agent review preferences',{exact:true}).waitFor();await page.waitForFunction(expected=>getComputedStyle(document.body).color===expected,mode==='dark'?'rgb(244, 244, 245)':'rgb(17, 24, 39)');await page.screenshot({path:join(process.env.CALNODE_SCREENSHOT_DIR,`agent-review-${mode}.png`)});await page.goto(origin+'/admin/connections');await page.getByText('Agent review preferences',{exact:true}).waitFor();}}
 assert.deepEqual(errors,[]);console.log('PASS: OAuth profiles, real MCP calls, human review, private/inactive import, replay prevention and personal preferences');
}finally{await browser.close();}
