import { afterEach, describe, expect, test, vi } from 'vitest';
import { render, cleanup } from 'vitest-browser-svelte';
import { get } from 'svelte/store';
import { currentUser } from '$lib/stores';
import { api, type User, type EventType } from '$lib/api';
import PermissionEditor from '$lib/components/PermissionEditor.svelte';
import OfferCustomTimes from '$lib/components/OfferCustomTimes.svelte';

const originalUser = get(currentUser);
afterEach(() => { cleanup(); vi.restoreAllMocks(); currentUser.set(originalUser); });

describe('scoped credential controls', () => {
 test('only shows delegable permissions within the current role ceiling', async () => {
  currentUser.set({id:'reader',email:'reader@example.com',name:'Reader',booking_accent:'neutral',timezone:'UTC',time_format:'12h',week_start:1,date_format:'mdy',is_admin:false,is_owner:false,role:'member',notify_confirmation:true,notify_cancellation:true,notify_reschedule:true,notify_reminder:true,notify_host_booking:true,notify_host_cancel:true,notify_host_reschedule:true,permissions: { 'bookings.read':'own', 'api_keys.create':'own' }} satisfies User);
  vi.spyOn(api, 'get').mockResolvedValue({items:[
   { id:'bookings.read',group:'bookings',scopes:['own','team','all'],delegable:true },
   { id:'api_keys.create',group:'api_keys',scopes:['own'],delegable:false },
   { id:'webhooks.create',group:'webhooks',scopes:['own'],delegable:true }
  ]});
  await render(PermissionEditor,{props:{value:{},delegated:true}});
  await expect.poll(()=>document.querySelectorAll('[aria-labelledby^="permission-"]').length).toBe(1);
  const trigger=document.querySelector<HTMLButtonElement>('[aria-labelledby^="permission-"]')!;trigger.click();
  await expect.poll(()=>document.querySelectorAll('[role="option"]').length).toBe(2);
  expect(document.body.textContent).not.toContain('Team resources');
 });
});

describe('custom time dialog',()=>{
 test.each(['light','dark'])('uses themed hour/minute pickers in %s mode',async(mode)=>{
  const wasDark=document.documentElement.classList.contains('dark');
  document.documentElement.classList.toggle('dark',mode==='dark');
  try {
   await render(OfferCustomTimes,{props:{open:true,event:{id:'event',name:'Intro call',duration_minutes:30} as EventType}});
   const dialog=document.querySelector<HTMLElement>('[role="dialog"]')!;
   expect(dialog.querySelector('input[type="time"]')).toBeNull();
   expect(dialog.querySelector('#custom-hour')).not.toBeNull();
   expect(dialog.querySelector('#custom-minute')).not.toBeNull();
   expect(dialog.scrollWidth).toBeLessThanOrEqual(dialog.clientWidth+1);
   expect([...dialog.querySelectorAll('button')].find(b=>b.textContent?.includes('Create & Copy'))?.disabled).toBe(true);
  } finally {document.documentElement.classList.toggle('dark',wasDark);}
 });
});
