import { afterEach, expect, test, vi } from 'vitest';
import { render, cleanup } from 'vitest-browser-svelte';
import { get } from 'svelte/store';
import { currentUser } from '$lib/stores';
import { api, type User } from '$lib/api';
import DynamicLinkDialog from '$lib/components/DynamicLinkDialog.svelte';
const original=get(currentUser);
afterEach(()=>{cleanup();vi.restoreAllMocks();currentUser.set(original);});
test.each(['light','dark'])('includes people without handles in %s mode',async(mode)=>{
 const dark=document.documentElement.classList.contains('dark');document.documentElement.classList.toggle('dark',mode==='dark');
 currentUser.set({id:'person-a',name:'Person A',handle:''} as User);
 const mint=vi.spyOn(api,'post').mockResolvedValue({url:'https://scheduler.test/t/single-use-test'});
 vi.spyOn(navigator.clipboard,'writeText').mockResolvedValue();
 vi.spyOn(api,'get').mockResolvedValue({items:[{id:'person-b',name:'Person B',handle:''}]});
 try {
  await render(DynamicLinkDialog,{props:{open:true}});
  await expect.poll(()=>document.querySelectorAll('[role="checkbox"]').length).toBe(2);
  for(const box of document.querySelectorAll<HTMLButtonElement>('[role="checkbox"]'))box.click();
  const copy=[...document.querySelectorAll<HTMLButtonElement>('button')].find(b=>b.textContent?.includes('Copy link'))!;
  await expect.poll(()=>copy.disabled).toBe(false);
  copy.click();await expect.poll(()=>mint.mock.calls.length).toBe(1);
  expect(mint).toHaveBeenCalledWith('/v1/dynamic-links',{host_ids:['person-a','person-b'],duration_minutes:30});
  await expect.poll(()=>document.body.textContent).toContain('/t/single-use-test');
  expect(document.querySelector('[role="dialog"]')!.textContent).not.toContain('No matching people');
 }finally{document.documentElement.classList.toggle('dark',dark);}
});
