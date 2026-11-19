<script lang="ts">
 import { onMount } from 'svelte';
 import VerifyIdentity from '$lib/components/VerifyIdentity.svelte';
 import SecurityOperations from '$lib/components/SecurityOperations.svelte';
 import { api } from '$lib/api';
 import { t } from '$lib/i18n';
 import { Button } from '$lib/components/ui/button';
 import { Input } from '$lib/components/ui/input';
 import { Label } from '$lib/components/ui/label';
 import { Switch } from '$lib/components/ui/switch';
 import * as Dialog from '$lib/components/ui/dialog';
 import { toast } from 'svelte-sonner';
 type Policy={password_min_length:number;require_admin_mfa:boolean;require_member_mfa:boolean;session_hours:number;dynamic_link_days:number;google_enabled:boolean;microsoft_enabled:boolean;members_sso_only:boolean;admin_password_enabled:boolean;auto_provision:boolean;approved_domains:string;required_group_id:string};
 type Response={policy:Policy;google_configured:boolean;microsoft_configured:boolean};
 let data=$state<Response|null>(null),error=$state(''),verify=$state(false),busy=$state(false),password=$state('');
 onMount(async()=>{try{data=await api.get<Response>('/v1/settings/security');}catch(e){error=e instanceof Error?e.message:$t('securitySettings.loadError');}});
 async function save(){busy=true;error='';try{
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
   <p class="text-xs leading-relaxed text-muted-foreground">{$t('identity.policyHint')} <a class="font-medium text-foreground underline underline-offset-4" href="/admin/settings/identity">{$t('identity.title')}</a></p>
   <div class="flex items-start justify-between gap-6 border-t pt-5"><div class="space-y-1"><Label for="members-sso">{$t('securitySettings.members')}</Label><p class="text-xs leading-relaxed text-muted-foreground">{$t('securitySettings.membersHint')}</p></div><Switch id="members-sso" bind:checked={data.policy.members_sso_only} /></div>
   <div class="flex items-start justify-between gap-6"><div class="space-y-1"><Label for="admin-password">{$t('securitySettings.password')}</Label><p class="text-xs leading-relaxed text-muted-foreground">{$t('securitySettings.passwordHint')}</p></div><Switch id="admin-password" bind:checked={data.policy.admin_password_enabled} /></div>
  </section>
  <section class="rounded-xl border bg-card p-5 space-y-4"><h3 class="text-sm font-semibold">{$t('nativeAuth.title')}</h3><p class="text-xs text-muted-foreground">{$t('nativeAuth.policyHint')}</p><div class="flex items-center justify-between gap-4"><Label for="require-admin-mfa">{$t('nativeAuth.adminMFA')}</Label><Switch id="require-admin-mfa" bind:checked={data.policy.require_admin_mfa} /></div><div class="flex items-center justify-between gap-4"><Label for="require-member-mfa">{$t('nativeAuth.memberMFA')}</Label><Switch id="require-member-mfa" bind:checked={data.policy.require_member_mfa} /></div><div class="grid gap-4 sm:grid-cols-2"><div class="space-y-2"><Label for="session-hours">{$t('nativeAuth.sessionHours')}</Label><Input id="session-hours" type="number" min={1} max={720} bind:value={data.policy.session_hours} /></div><div class="space-y-2"><Label for="dynamic-expiry">{$t('nativeAuth.linkDays')}</Label><Input id="dynamic-expiry" type="number" min={1} max={30} bind:value={data.policy.dynamic_link_days} /></div></div></section>
  <section class="space-y-4 rounded-xl border bg-card p-5"><div><h3 class="text-sm font-semibold">{$t('passwordPolicy.title')}</h3><p class="mt-1 text-xs leading-relaxed text-muted-foreground">{$t('passwordPolicy.description')}</p></div><div class="max-w-xs space-y-2"><Label for="password-minimum">{$t('passwordPolicy.minimum')}</Label><Input id="password-minimum" type="number" min={15} max={128} bind:value={data.policy.password_min_length} /></div><p class="text-xs leading-relaxed text-muted-foreground">{$t('passwordPolicy.application')}</p></section>
  <p class="text-xs leading-relaxed text-muted-foreground">{$t('identity.sessionHint')}</p>
  <Button type="button" onclick={()=>{error='';password='';verify=true;}}>{$t('securitySettings.save')}</Button>
  <SecurityOperations />
 {:else if !error}<p class="text-sm text-muted-foreground">{$t('securitySettings.loading')}</p>{/if}
</div>
<Dialog.Root bind:open={verify}>
 <Dialog.Content class="sm:max-w-md"><Dialog.Header><Dialog.Title>{$t('securitySettings.verify')}</Dialog.Title><Dialog.Description>{$t('securitySettings.verifyHint')}</Dialog.Description></Dialog.Header>
  <form onsubmit={e=>{e.preventDefault();save();}} class="space-y-4"><VerifyIdentity />
   {#if error}<p role="alert" class="text-sm text-destructive">{error}</p>{/if}
   <Dialog.Footer><Button type="button" variant="outline" disabled={busy} onclick={()=>verify=false}>{$t('securitySettings.cancel')}</Button><Button type="submit" disabled={busy}>{$t('securitySettings.confirm')}</Button></Dialog.Footer>
  </form>
 </Dialog.Content>
</Dialog.Root>
