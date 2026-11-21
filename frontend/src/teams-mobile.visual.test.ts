import { afterEach, expect, test, vi } from 'vitest';
import { cleanup, render } from 'vitest-browser-svelte';
import { page } from 'vitest/browser';
import MembersRoles from '$lib/components/MembersRoles.svelte';
import TeamsManager from '$lib/components/TeamsManager.svelte';
import { api, type User } from '$lib/api';
import { currentUser } from '$lib/stores';

const name='Alexandria Montgomery-Wellington';
const email='alexandria.montgomery-wellington@mobile-audit.example.test';
const team={id:'ops',name:'Corporate Operations & Client Services',slug:'corporate-operations-client-services',member_count:1,my_role:'member',members:[{id:'sam',name:'Sam Taylor',email:'sam.taylor@mobile-audit.example.test',role:'member'}]};
function fixture(admin=true) {
 currentUser.set({id:'me',name:'Owner',is_admin:admin,permissions:{'members.read':'all','teams.read':'all','teams.members':'all','teams.update':'team'}} as unknown as User);
 vi.spyOn(api,'get').mockImplementation(async path=>path==='/v1/teams'?{items:[team]}:path==='/v1/teams/ops'?team:[{id:'sam',name:'Sam Taylor'},{id:'long',name,email}] as never);
}
afterEach(async()=>{cleanup();vi.restoreAllMocks();currentUser.set(null);document.documentElement.classList.remove('dark');await page.viewport(1280,720);});

for(const width of [320,390]) for(const dark of [false,true]) test(`team picker and member actions fit ${width}px ${dark?'dark':'light'}`,async()=>{
 await page.viewport(width,844);document.documentElement.classList.toggle('dark',dark);fixture();
 const post=vi.spyOn(api,'post').mockImplementation(async()=>{team.member_count=2;team.members.push({id:'long',name,email,role:'member'});return {} as never;});
 const {container}=await render(TeamsManager);
 await page.getByRole('button',{name:'Manage',exact:true}).click();
 await page.getByRole('button',{name:'Add a member…',exact:true}).click();
 await page.getByPlaceholder('Search name or email…').fill('montgomery-wellington@');
 await page.getByRole('button',{name:new RegExp(name)}).click();
 const picker=container.querySelector<HTMLElement>('[data-slot="combobox-trigger"]')!;
 expect(picker.textContent).toContain(name);expect(picker.textContent).not.toContain(email);
 await page.getByRole('button',{name:'Add',exact:true}).click();
 await expect.poll(()=>post.mock.calls.length).toBe(1);
 expect(post.mock.calls[0]).toEqual(['/v1/teams/ops/members',{user_id:'long'}]);
 await expect.poll(()=>container.textContent).toContain(email);
 const remove=container.querySelector<HTMLElement>('[aria-label="Remove '+name+' from team"]')!;
 expect(remove.getBoundingClientRect().height).toBeGreaterThanOrEqual(44);
 for(const el of container.querySelectorAll<HTMLElement>('input,textarea,button,[role="combobox"]')) {
  const r=el.getBoundingClientRect();if(!r.width||!r.height)continue;
  expect(r.right,el.outerHTML).toBeLessThanOrEqual(width+1);expect(r.left).toBeGreaterThanOrEqual(-1);
 }
 expect(document.documentElement.scrollWidth).toBeLessThanOrEqual(width);
 team.members=team.members.filter(m=>m.id!=='long');team.member_count=1;
});

test('a regular member does not see configuration controls even with a team update grant',async()=>{
 fixture(false);const {container}=await render(TeamsManager);
 await page.getByRole('button',{name:'Manage',exact:true}).click();
 await expect.poll(()=>container.textContent).toContain('Sam Taylor');
 expect(container.textContent).not.toContain('Save profile');
 expect(container.textContent).not.toContain('Rename');
 expect(container.querySelector('[data-slot="combobox-trigger"]')).toBeNull();
});

for(const width of [320,390]) test(`member administration and invitations stay reachable at ${width}px`,async()=>{
 await page.viewport(width,844);
 currentUser.set({id:'me',name:'Owner',is_admin:true,permissions:{'members.read':'all','members.invite_member':'all','members.update_role':'all','members.password':'all','members.reset_mfa':'all','members.archive':'all'}} as unknown as User);
 vi.spyOn(api,'get').mockImplementation(async path=>path==='/v1/users'?[{id:'other',name,email,role:'member',email_login:true,teams:[{id:'ops',name:team.name}],created_at:'2026-11-01T12:00:00Z'}]:path==='/v1/invites'?[{id:'inv',email:'another.long.invited.person@mobile-audit.example.test',expires_at:'2099-01-01T12:00:00Z'}]:{items:[]} as never);
 const {container}=await render(MembersRoles);
 await expect.poll(()=>container.textContent).toContain(name);
 for(const el of container.querySelectorAll<HTMLElement>('.member-table button')) {
  const r=el.getBoundingClientRect();expect(r.left).toBeGreaterThanOrEqual(0);expect(r.right).toBeLessThanOrEqual(width);expect(r.height).toBeGreaterThanOrEqual(44);
 }
 expect(container.querySelector('.member-table')!.getBoundingClientRect().width).toBeLessThanOrEqual(width);
 expect(document.documentElement.scrollWidth).toBeLessThanOrEqual(width);
});
