<script lang="ts">
 import { onMount } from 'svelte';
 import { api } from '$lib/api';
 import { t } from '$lib/i18n';
 import { Button } from '$lib/components/ui/button';
 import { Input } from '$lib/components/ui/input';
 import { Label } from '$lib/components/ui/label';
 import { Switch } from '$lib/components/ui/switch';
 import { Badge } from '$lib/components/ui/badge';
 import * as Dialog from '$lib/components/ui/dialog';
 import { toast } from 'svelte-sonner';
 type Policy={google_enabled:boolean;microsoft_enabled:boolean;members_sso_only:boolean;admin_password_enabled:boolean;auto_provision:boolean;approved_domains:string;required_group_id:string};
 type Response={policy:Policy;google_configured:boolean;microsoft_configured:boolean};
 let data=$state<Response|null>(null),error=$state(''),verify=$state(false),busy=$state(false),password=$state('');
 onMount(async()=>{try{data=await api.get<Response>('/v1/settings/security');}catch(e){error=e instanceof Error?e.message:$t('securitySettings.loadError');}});
 async function save(){busy=true;error='';try{
  await api.post('/v1/auth/reauthenticate',{password});
  data=await api.put<Response>('/v1/settings/security',data!.policy);
  verify=false;password='';toast.success($t('securitySettings.saved'));
 }catch(e){error=e instanceof Error?e.message:$t('securitySettings.saveError');}finally{busy=false;}}
</script>
<svelte:head><title>{$t('securitySettings.title')}</title></svelte:head>
<div class="max-w-2xl space-y-6">
 <header><h2 class="text-xl font-semibold tracking-tight">{$t('securitySettings.title')}</h2><p class="mt-1 text-sm text-muted-foreground">{$t('securitySettings.subtitle')}</p></header>
 {#if error && !verify}<p role="alert" class="text-sm text-destructive">{error}</p>{/if}
 {#if data}
  <section class="rounded-xl border bg-card p-5 space-y-5" aria-labelledby="signin-methods">
   <h3 id="signin-methods" class="text-sm font-semibold">{$t('securitySettings.methods')}</h3>
   {#each ['microsoft','google'] as provider}
    {@const microsoft=provider==='microsoft'}
    {@const configured=microsoft?data.microsoft_configured:data.google_configured}
    <div class="flex items-start justify-between gap-6">
     <div class="space-y-1"><Label for={'signin-'+provider}>{$t(microsoft?'securitySettings.microsoft':'securitySettings.google')}</Label><p class="text-xs leading-relaxed text-muted-foreground">{$t(microsoft?'securitySettings.microsoftHint':'securitySettings.googleHint')}</p><Badge variant="secondary" class="text-[11px]">{$t(configured?'securitySettings.configured':'securitySettings.notConfigured')}</Badge></div>
     {#if microsoft}<Switch id="signin-microsoft" bind:checked={data.policy.microsoft_enabled} />{:else}<Switch id="signin-google" bind:checked={data.policy.google_enabled} />{/if}
    </div>
   {/each}
   <div class="flex items-start justify-between gap-6 border-t pt-5"><div class="space-y-1"><Label for="members-sso">{$t('securitySettings.members')}</Label><p class="text-xs leading-relaxed text-muted-foreground">{$t('securitySettings.membersHint')}</p></div><Switch id="members-sso" bind:checked={data.policy.members_sso_only} /></div>
   <div class="flex items-start justify-between gap-6"><div class="space-y-1"><Label for="admin-password">{$t('securitySettings.password')}</Label><p class="text-xs leading-relaxed text-muted-foreground">{$t('securitySettings.passwordHint')}</p></div><Switch id="admin-password" bind:checked={data.policy.admin_password_enabled} /></div>
  </section>
  <section class="rounded-xl border bg-card p-5 space-y-5" aria-labelledby="automatic-enrollment">
   <h3 id="automatic-enrollment" class="text-sm font-semibold">{$t('securitySettings.enrollment')}</h3>
   <div class="flex items-start justify-between gap-6"><div class="space-y-1"><Label for="auto-enrollment">{$t('securitySettings.auto')}</Label><p class="text-xs leading-relaxed text-muted-foreground">{$t('securitySettings.autoHint')}</p></div><Switch id="auto-enrollment" bind:checked={data.policy.auto_provision} /></div>
   <div class="space-y-2"><Label for="approved-domains">{$t('securitySettings.domains')}</Label><Input id="approved-domains" bind:value={data.policy.approved_domains} placeholder="company.com" /><p class="text-xs leading-relaxed text-muted-foreground">{$t('securitySettings.domainsHint')}</p></div>
   <div class="space-y-2"><Label for="security-group">{$t('securitySettings.group')}</Label><Input id="security-group" bind:value={data.policy.required_group_id} placeholder="00000000-0000-0000-0000-000000000000" /><p class="text-xs leading-relaxed text-muted-foreground">{$t('securitySettings.groupHint')}</p></div>
   <div class="rounded-lg border bg-muted/30 p-3 text-xs leading-relaxed"><p class="font-medium">{$t('securitySettings.invites')}</p><p class="mt-1 text-muted-foreground">{$t('securitySettings.inviteHint')}</p></div>
  </section>
  <div class="space-y-2 text-xs leading-relaxed text-muted-foreground"><p>{$t('securitySettings.legacy')}</p><p>{$t('securitySettings.sessions')}</p></div>
  <Button type="button" onclick={()=>{error='';password='';verify=true;}}>{$t('securitySettings.save')}</Button>
 {:else if !error}<p class="text-sm text-muted-foreground">{$t('securitySettings.loading')}</p>{/if}
</div>
<Dialog.Root bind:open={verify}>
 <Dialog.Content class="sm:max-w-md"><Dialog.Header><Dialog.Title>{$t('securitySettings.verify')}</Dialog.Title><Dialog.Description>{$t('securitySettings.verifyHint')}</Dialog.Description></Dialog.Header>
  <form onsubmit={e=>{e.preventDefault();save();}} class="space-y-4"><div class="space-y-2"><Label for="security-password">{$t('securitySettings.currentPassword')}</Label><Input id="security-password" type="password" autocomplete="current-password" bind:value={password} /></div>
   {#if error}<p role="alert" class="text-sm text-destructive">{error}</p>{/if}
   <Dialog.Footer><Button type="button" variant="outline" disabled={busy} onclick={()=>verify=false}>{$t('securitySettings.cancel')}</Button><Button type="submit" disabled={busy}>{$t('securitySettings.confirm')}</Button></Dialog.Footer>
  </form>
 </Dialog.Content>
</Dialog.Root>
