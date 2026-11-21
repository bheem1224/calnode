import { afterEach, expect, test, vi } from 'vitest';
import { cleanup, render } from 'vitest-browser-svelte';
import { api } from '$lib/api';
import { currentUser } from '$lib/stores';
import WebhookScopePicker from '$lib/components/WebhookScopePicker.svelte';
import EmailTemplateLibrary from '$lib/components/EmailTemplateLibrary.svelte';

afterEach(()=>{cleanup();vi.restoreAllMocks();currentUser.set(null);});
test.each(['light','dark'])('template library edits the existing template through the shared builder in %s',async mode=>{
 const previous=document.documentElement.classList.contains('dark');document.documentElement.classList.toggle('dark',mode==='dark');
 const item={id:'template',name:'Client notice',kind:'confirmation',mode:'html',subject:'Hello {Event}',body:'<p>Original copy</p>',is_default:true,created_at:'',updated_at:''};
 vi.spyOn(api,'get').mockResolvedValue({items:[item]});
 vi.spyOn(api,'post').mockResolvedValue({subject:'Preview',html:'<p>Preview</p>',default_html:'<p>Default</p>'});
 const patch=vi.spyOn(api,'patch').mockResolvedValue(item);
 try{
  const {container}=await render(EmailTemplateLibrary);
  await expect.poll(()=>container.textContent).toContain('Client notice');
  [...container.querySelectorAll('button')].find(b=>b.getAttribute('aria-label')==='Edit')!.click();
  await expect.poll(()=>document.querySelector('textarea')?.value).toBe('<p>Original copy</p>');
  const dialog=document.querySelector<HTMLElement>('[role="dialog"]')!;
  const editor=dialog.querySelector('textarea')!;editor.value='<p>Updated copy</p>';editor.dispatchEvent(new Event('input',{bubbles:true}));
  [...dialog.querySelectorAll('button')].find(b=>b.textContent?.trim()==='Save template')!.click();
  await expect.poll(()=>patch.mock.calls.length).toBe(1);
  expect(patch.mock.calls[0]).toEqual(['/v1/email-templates/template',expect.objectContaining({mode:'html',body:'<p>Updated copy</p>',is_default:true})]);
  expect(container.scrollWidth).toBeLessThanOrEqual(document.documentElement.clientWidth);
 }finally{document.documentElement.classList.toggle('dark',previous);}
});

test.each(['light','dark'])('webhook scope picker keeps multiple checked choices without overflow in %s',async mode=>{
 const previous=document.documentElement.classList.contains('dark');document.documentElement.classList.toggle('dark',mode==='dark');
 try{
  const {container}=await render(WebhookScopePicker,{props:{value:[],items:[{type:'team',id:'team',name:'A team with a long descriptive name '.repeat(3)},{type:'event',id:'event',name:'Client intro'},{type:'instance',name:''}]}});
  container.querySelector('button')!.click();
  await expect.poll(()=>document.querySelectorAll('[role="checkbox"]').length).toBe(3);
  const boxes=[...document.querySelectorAll<HTMLButtonElement>('[role="checkbox"]')];boxes[0].click();boxes[1].click();
  await expect.poll(()=>boxes.filter(b=>b.getAttribute('aria-checked')==='true').length).toBe(2);
  expect(container.textContent).toContain('2 scopes selected');
  expect(document.documentElement.scrollWidth).toBeLessThanOrEqual(innerWidth);
 }finally{document.documentElement.classList.toggle('dark',previous);}
});
