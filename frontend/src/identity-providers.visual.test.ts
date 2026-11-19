import { afterEach, expect, test, vi } from 'vitest';
import { render, cleanup } from 'vitest-browser-svelte';
import IdentityProviders from './routes/settings/identity/+page.svelte';
import { api } from '$lib/api';

afterEach(()=>{cleanup();vi.restoreAllMocks();});
const provider={id:'company',name:'Company sign-in',kind:'microsoft',purpose:'sso',enabled:true,allow_members:true,client_id:'client-id',client_secret_set:true,microsoft_tenant_id:'11111111-1111-4111-8111-111111111111',issuer_url:'',pkce:true,auto_provision:true,approved_domains:['company.com'],group_ids:['11111111-1111-4111-8111-111111111111'],group_match:'any',groups_claim:'groups',legacy_key:''};

test.each(['light','dark'])('provider editor has compact controls and safe review in %s mode',async mode=>{
 const dark=document.documentElement.classList.contains('dark');document.documentElement.classList.toggle('dark',mode==='dark');
 vi.spyOn(api,'get').mockResolvedValue({items:[provider],base_url:'https://cal.example.com'});
 const save=vi.spyOn(api,'put').mockResolvedValue(provider);
 try{
  const {container}=await render(IdentityProviders);
  await expect.poll(()=>container.querySelector('section')?.textContent).toContain(provider.name);
  container.querySelector<HTMLButtonElement>('section button')!.click();
  await expect.poll(()=>document.querySelector('#provider-groups')).not.toBeNull();
  const dialog=document.querySelector<HTMLElement>('[role="dialog"]')!;
  expect(dialog.scrollWidth).toBeLessThanOrEqual(dialog.clientWidth);
  expect(dialog.textContent).toContain('https://cal.example.com/v1/auth/providers/company/callback');
  const input=dialog.querySelector<HTMLInputElement>('#provider-groups')!;input.value='22222222-2222-4222-8222-222222222222';input.dispatchEvent(new Event('input',{bubbles:true}));
  input.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true}));
  await expect.poll(()=>dialog.querySelector('#provider-match')?.textContent).toContain('Any listed group');
  const review=[...dialog.querySelectorAll<HTMLButtonElement>('button')].find(b=>b.textContent?.includes('Review changes'))!;review.click();
  await expect.poll(()=>dialog.querySelector('#verify-password')).not.toBeNull();
  expect(save).not.toHaveBeenCalled();
  const submit=dialog.querySelector<HTMLButtonElement>('button[type="submit"]')!;submit.click();
  await expect.poll(()=>save.mock.calls.length).toBe(1);
  expect(save).toHaveBeenCalledWith('/v1/settings/identity-providers/company',expect.objectContaining({group_match:'any',group_ids:[provider.group_ids[0],'22222222-2222-4222-8222-222222222222'],client_secret:''}));
 }finally{document.documentElement.classList.toggle('dark',dark);}
});
