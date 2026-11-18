import { api } from '$lib/api';
const decode=(s:string)=>Uint8Array.from(atob(s.replace(/-/g,'+').replace(/_/g,'/')),c=>c.charCodeAt(0));
const encode=(b:ArrayBuffer)=>btoa(String.fromCharCode(...new Uint8Array(b))).replace(/\+/g,'-').replace(/\//g,'_').replace(/=+$/,'');
function serialize(credential:PublicKeyCredential){
 const r=credential.response;
 const out:Record<string,unknown>={clientDataJSON:encode(r.clientDataJSON)};
 if('attestationObject' in r){out.attestationObject=encode((r as AuthenticatorAttestationResponse).attestationObject);out.transports=(r as AuthenticatorAttestationResponse).getTransports?.()||[];}
 else{const a=r as AuthenticatorAssertionResponse;out.authenticatorData=encode(a.authenticatorData);out.signature=encode(a.signature);out.userHandle=a.userHandle?encode(a.userHandle):null;}
 return {id:credential.id,rawId:encode(credential.rawId),type:credential.type,response:out,clientExtensionResults:credential.getClientExtensionResults(),authenticatorAttachment:credential.authenticatorAttachment};
}
async function send(path:string,credential:PublicKeyCredential,headers:Record<string,string>={}){
 const res=await fetch(path,{method:'POST',headers:{'Content-Type':'application/json',...headers},body:JSON.stringify(serialize(credential))});
 if(!res.ok){const e=await res.json();throw new Error(e.error||'Passkey verification failed');}return res.json();
}
export async function registerPasskey(name:string){
 const {options,token}=await api.post<{options:any;token:string}>('/v1/users/me/security/passkeys/begin',{});
 const p=options.publicKey;p.challenge=decode(p.challenge);p.user.id=decode(p.user.id);p.excludeCredentials=p.excludeCredentials?.map((c:any)=>({...c,id:decode(c.id)}));
 const credential=await navigator.credentials.create({publicKey:p}) as PublicKeyCredential|null;if(!credential)throw new Error('Passkey registration cancelled');
 return send('/v1/users/me/security/passkeys/finish',credential,{'X-Auth-Challenge':token,'X-Passkey-Name':name||'Passkey'});
}
export async function signInWithPasskey(stepUp=false){
 const suffix=stepUp?'?step_up=1':'';
 const {options}=await api.post<{options:any}>('/v1/auth/passkeys/begin'+suffix,{});
 const p=options.publicKey;p.challenge=decode(p.challenge);p.allowCredentials=p.allowCredentials?.map((c:any)=>({...c,id:decode(c.id)}));
 const credential=await navigator.credentials.get({publicKey:p}) as PublicKeyCredential|null;if(!credential)throw new Error('Passkey sign-in cancelled');
 return send('/v1/auth/passkeys/finish'+suffix,credential);
}
export function redirectAuthStep(res:Response){const step=res.headers.get('X-Calnode-Auth-Step');if(step==='mfa'||step==='enroll'){window.location.assign('/admin/auth/'+step);return true;}return false;}
