import { afterEach, expect, test, vi } from 'vitest';
import { render, cleanup } from 'vitest-browser-svelte';
import { api } from '$lib/api';
import AgentReviewPreferences from '$lib/components/AgentReviewPreferences.svelte';
import Review from './routes/connections/review/+page.svelte';

afterEach(()=>{cleanup();vi.restoreAllMocks();});
test.each(['light','dark'])('company review policy stays enforced without overflow in %s',async mode=>{
 const dark=document.documentElement.classList.contains('dark');document.documentElement.classList.toggle('dark',mode==='dark');
 vi.spyOn(api,'get').mockImplementation(async path=>path.includes('mcp-preferences')?{review_required:false,enforced:true,effective_review_required:true}:{items:[]});
 const put=vi.spyOn(api,'put').mockResolvedValue({});
 try{const {container}=await render(AgentReviewPreferences);await expect.poll(()=>container.textContent).toContain('Your company requires Calnode review.');
 const toggle=container.querySelector('[role="switch"]') as HTMLButtonElement;expect(toggle.disabled).toBe(true);expect(toggle.getAttribute('aria-checked')).toBe('true');
 const card=container.firstElementChild as HTMLElement;expect(card.scrollWidth).toBeLessThanOrEqual(card.clientWidth);expect(put).not.toHaveBeenCalled();
 }finally{document.documentElement.classList.toggle('dark',dark);}
});
test.each(['light','dark'])('review shows immutable fields and requires an explicit confirmation in %s',async mode=>{
 const dark=document.documentElement.classList.contains('dark');document.documentElement.classList.toggle('dark',mode==='dark');
 vi.spyOn(api,'get').mockResolvedValue({id:'proposal',status:'pending',expires_at:new Date(Date.now()+600000).toISOString(),operations:[{name:'update_event_type',target:'intro',before:{name:'Intro'},values:{name:'Updated intro',description:'<script>untrusted</script>'+('long-content '.repeat(80))}}],results:[]});
 const post=vi.spyOn(api,'post').mockResolvedValue({});
 try{const {container}=await render(Review);await expect.poll(()=>container.textContent).toContain('Updated intro');expect(container.querySelector('script')).toBeNull();
 const card=container.firstElementChild as HTMLElement;expect(card.scrollWidth).toBeLessThanOrEqual(card.clientWidth);
 const approve=[...container.querySelectorAll('button')].find(b=>b.textContent?.includes('Approve these changes'))!;approve.click();
 await expect.poll(()=>document.querySelector('[role="alertdialog"]')).not.toBeNull();expect(post).not.toHaveBeenCalled();
 }finally{document.documentElement.classList.toggle('dark',dark);}
});
