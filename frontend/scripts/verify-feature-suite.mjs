// One command for the branch's real-server browser regressions. Each script gets
// its own disposable in-memory instance; configured/live servers are never used.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { once } from 'node:events';
import { createServer } from 'node:net';
import { createWriteStream } from 'node:fs';
import { mkdir, mkdtemp } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root=resolve(dirname(fileURLToPath(import.meta.url)),'../..');
const binary=process.env.CALNODE_TEST_BINARY;
assert.ok(binary,'Build Calnode and set CALNODE_TEST_BINARY to the binary path');
const identityOnly=process.argv.includes('--identity-only');
assert.ok(process.argv.slice(2).every(arg=>arg==='--identity-only'),'Unknown verification option');
const output=await mkdtemp(join(tmpdir(),'calnode-feature-suite-'));
const env={...process.env};
for(const key of Object.keys(env))if(/^(EMAIL_|SMTP_|RESEND_|GOOGLE_|MICROSOFT_|LITESTREAM_|STRIPE_|LIVEKIT_|ZOOM_|DATABASE_URL$|BASE_URL$|PUBLIC_BASE_URL$|PORT$|DEMO_MODE$|CALNODE_)/.test(key))delete env[key];
env.CALNODE_TEST_BINARY=resolve(binary);
env.CALNODE_SCREENSHOT_DIR=process.env.CALNODE_SCREENSHOT_DIR||join(output,'screenshots');
await mkdir(env.CALNODE_SCREENSHOT_DIR,{recursive:true});
async function run(script,extra={}){
 const child=spawn(process.execPath,[join(root,'frontend/scripts',script)],{cwd:output,env:{...env,...extra},stdio:'inherit',windowsHide:true});
 const [code]=await once(child,'exit');assert.equal(code,0,script+' failed');
}
for(const [script,urlVariable]of [['verify-native-auth.mjs','CALNODE_AUTH_TEST_URL'],['verify-identity-providers.mjs','CALNODE_IDENTITY_TEST_URL'],['verify-agent-operations.mjs','CALNODE_AGENT_TEST_URL']]){
 if(identityOnly&&script!=='verify-identity-providers.mjs')continue;
 const socket=createServer();socket.listen(0,'127.0.0.1');await once(socket,'listening');const port=socket.address().port;await new Promise(r=>socket.close(r));
 const origin='http://localhost:'+port;
 const log=createWriteStream(join(output,script+'.server.log'));
 const server=spawn(env.CALNODE_TEST_BINARY,[],{cwd:output,env:{...env,PORT:String(port),BASE_URL:origin,PUBLIC_BASE_URL:origin,DATA_DIR:output,DATABASE_URL:'sqlite://:memory:',DEMO_MODE:'false',CALNODE_ENCRYPTION_KEY:randomBytes(32).toString('hex')},stdio:['ignore','pipe','pipe'],windowsHide:true});
 const closed=once(server,'close');server.stdout.pipe(log,{end:false});server.stderr.pipe(log,{end:false});
 try{
  const deadline=Date.now()+60000;let ready=false;
  while(Date.now()<deadline&&server.exitCode===null){
   let response;try{response=await fetch(origin+'/v1/auth/status',{redirect:'error'});}catch{}
   if(response?.ok){assert.equal((await response.json()).claimed,false,'Refusing an existing instance');ready=true;break;}
   await new Promise(r=>setTimeout(r,100));
  }
  assert.ok(ready,'Disposable test server did not start');
  console.log('Running '+script);
  await run(script,{[urlVariable]:origin});
 }finally{
  if(server.exitCode===null)server.kill();
  const force=setTimeout(()=>server.kill('SIGKILL'),10000);force.unref();
  try{await closed;}finally{clearTimeout(force);log.end();}
 }
}
if(!identityOnly)await run('verify-booking-flows.mjs');
console.log('PASS: isolated feature browser suite');
