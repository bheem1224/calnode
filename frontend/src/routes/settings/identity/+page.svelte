<script lang="ts">
 import { onMount } from 'svelte';
 import { api } from '$lib/api';
 import { t } from '$lib/i18n';
 import { Button } from '$lib/components/ui/button';
 import { Input } from '$lib/components/ui/input';
 import { Label } from '$lib/components/ui/label';
 import { Badge } from '$lib/components/ui/badge';
 import { Switch } from '$lib/components/ui/switch';
 import * as Dialog from '$lib/components/ui/dialog';
 import * as Select from '$lib/components/ui/select';
 import VerifyIdentity from '$lib/components/VerifyIdentity.svelte';
 import ProviderValues from '$lib/components/ProviderValues.svelte';
 import { toast } from 'svelte-sonner';
 type Provider={revision?:number;id:string;name:string;kind:string;purpose:string;enabled:boolean;allow_members:boolean;client_id:string;client_secret_set:boolean;microsoft_tenant_id:string;issuer_url:string;pkce:boolean;auto_provision:boolean;approved_domains:string[];group_ids:string[];group_match:string;groups_claim:string;legacy_key:string};
 let items=$state<Provider[]>([]),loading=$state(true),error=$state(''),open=$state(false),review=$state(false),busy=$state(false),origin=$state(''),secret=$state('');
 function blank():Provider{return {id:'',name:'',kind:'microsoft',purpose:'sso',enabled:true,allow_members:true,client_id:'',client_secret_set:false,microsoft_tenant_id:'',issuer_url:'',pkce:true,auto_provision:false,approved_domains:[],group_ids:[],group_match:'any',groups_claim:'groups',legacy_key:''};}
 let draft=$state<Provider>(blank());
 const hasSSO=$derived(draft.purpose!=='integrations');
 const hasCalendar=$derived(draft.purpose!=='sso'&&draft.kind!=='oidc');
 const baseURL=$derived(origin.replace(/\/+$/,''));
 const callback=$derived(baseURL+(draft.legacy_key==='google'?'/v1/auth/callback':draft.legacy_key==='microsoft'?'/v1/auth/microsoft/callback':`/v1/auth/providers/${draft.id}/callback`));
 const calendarCallback=$derived(baseURL+'/v1/calendar/callback');
 const purposes=$derived([{value:'sso',label:$t('identity.sso')},{value:'both',label:$t('identity.both')},{value:'integrations',label:$t('identity.integrations')}]);
 const kinds=[{value:'microsoft',label:'Microsoft Entra ID'},{value:'google',label:'Google'},{value:'oidc',label:'OpenID Connect (OIDC)'}];
 onMount(async()=>{await load();const id=new URL(window.location.href).searchParams.get('draft');if(id){try{const proposed=await api.get<Partial<Provider>>('/v1/mcp/integration-drafts/'+encodeURIComponent(id));edit();draft={...blank(),...proposed,id:'',enabled:false};}catch(e){error=e instanceof Error?e.message:$t('common.failed');}}});
 async function load(){try{const result=await api.get<{items:Provider[];base_url:string}>('/v1/settings/identity-providers');items=result.items;origin=result.base_url;}catch(e){error=e instanceof Error?e.message:$t('common.failed');}finally{loading=false;}}
 function edit(provider?:Provider){draft=provider?structuredClone($state.snapshot(provider)):blank();secret='';error='';review=false;open=true;}
 function changeKind(kind:string){draft.kind=kind;if(kind==='oidc')draft.purpose='sso';draft.group_ids=[];}
 async function copyCallback(value:string){try{await navigator.clipboard.writeText(value);toast.success($t('identity.callbackCopied'));}catch{toast.error($t('identity.callbackCopyFailed'));}}
 async function save(){busy=true;error='';try{const payload={...draft,auto_provision:hasSSO&&draft.auto_provision,client_secret:secret};if(draft.id){await api.put('/v1/settings/identity-providers/'+draft.id,payload);open=false;}else{draft=await api.post<Provider>('/v1/settings/identity-providers',payload);review=false;}secret='';await load();toast.success($t('identity.saved'));}catch(e){error=e instanceof Error?e.message:$t('common.failed');}finally{busy=false;}}
</script>
<svelte:head><title>{$t('identity.title')}</title></svelte:head>
<div class="max-w-3xl space-y-5">
 <header class="flex flex-wrap items-start justify-between gap-4"><div class="max-w-xl"><h2 class="text-xl font-semibold tracking-tight">{$t('identity.title')}</h2><p class="mt-1 text-sm leading-relaxed text-muted-foreground">{$t('identity.subtitle')}</p></div><Button type="button" onclick={()=>edit()}>+ {$t('identity.add')}</Button></header>
 {#if error&&!open}<p role="alert" class="text-sm text-destructive">{error}</p>{/if}
 {#if loading}<p class="text-sm text-muted-foreground">{$t('securitySettings.loading')}</p>{:else if !items.length}<div class="rounded-xl border border-dashed p-8 text-center text-sm text-muted-foreground">{$t('identity.empty')}</div>{/if}
 <div class="space-y-3">{#each items as provider (provider.id)}<section class="flex flex-wrap items-center justify-between gap-4 rounded-xl border bg-card p-5"><div class="min-w-0 space-y-2"><h3 class="break-words text-sm font-semibold">{provider.name}</h3><div class="flex flex-wrap gap-2"><Badge variant="outline">{kinds.find(k=>k.value===provider.kind)?.label}</Badge><Badge variant="secondary">{purposes.find(p=>p.value===provider.purpose)?.label}</Badge>{#if !provider.enabled}<Badge variant="outline">{$t('identity.disabled')}</Badge>{/if}</div></div><Button type="button" variant="outline" size="sm" onclick={()=>edit(provider)} aria-label={$t('identity.editProvider',{name:provider.name})}>{$t('identity.edit')}</Button></section>{/each}</div>
 <p class="text-xs leading-relaxed text-muted-foreground">{$t('identity.separateHint')}</p>
 <a class="text-sm font-medium underline underline-offset-4" href="/admin/settings/security">{$t('identity.sharedPolicy')}</a>
</div>
<Dialog.Root bind:open onOpenChange={value=>{if(!value)secret='';}}>
 <Dialog.Content class="max-h-[90dvh] overflow-y-auto sm:max-w-xl">
  <Dialog.Header><Dialog.Title>{draft.id?$t('identity.edit'):$t('identity.add')}</Dialog.Title><Dialog.Description>{$t(review?'identity.reviewHint':'identity.setupHint')}</Dialog.Description></Dialog.Header>
  <form class="min-w-0 space-y-5" onsubmit={e=>{e.preventDefault();if(review)save();else review=true;}}>
   {#if !review}
    <div class="grid gap-4 sm:grid-cols-2"><div class="space-y-2"><Label for="provider-kind">{$t('identity.provider')}</Label><Select.Root type="single" value={draft.kind} onValueChange={changeKind} disabled={!!draft.id}><Select.Trigger id="provider-kind" class="w-full justify-between">{kinds.find(k=>k.value===draft.kind)?.label}</Select.Trigger><Select.Content class="min-w-(--bits-select-anchor-width)">{#each kinds as kind}<Select.Item value={kind.value}>{kind.label}</Select.Item>{/each}</Select.Content></Select.Root></div><div class="space-y-2"><Label for="provider-purpose">{$t('identity.useFor')}</Label><Select.Root type="single" bind:value={draft.purpose} disabled={draft.kind==='oidc'}><Select.Trigger id="provider-purpose" class="w-full justify-between">{purposes.find(p=>p.value===draft.purpose)?.label}</Select.Trigger><Select.Content class="min-w-(--bits-select-anchor-width)">{#each purposes as purpose}<Select.Item value={purpose.value}>{purpose.label}</Select.Item>{/each}</Select.Content></Select.Root></div></div>
    <div class="rounded-lg border bg-muted/30 p-3 text-xs leading-relaxed text-muted-foreground"><p>{$t(`identity.instructions.${draft.kind}`)}</p>{#if draft.kind!=='oidc'}<a class="mt-2 inline-block font-medium text-foreground underline underline-offset-4" href={draft.kind==='microsoft'?'https://learn.microsoft.com/en-us/entra/identity-platform/quickstart-register-app':'https://developers.google.com/identity/protocols/oauth2/web-server'} target="_blank" rel="noopener noreferrer">{$t('identity.providerDocs')}</a>{/if}</div>
    <div class="space-y-2"><Label for="provider-name">{$t('identity.name')}</Label><Input id="provider-name" maxlength={80} bind:value={draft.name} required placeholder={$t('identity.namePlaceholder')} /></div>
    {#if draft.kind==='microsoft'}<div class="space-y-2"><Label for="provider-tenant">{$t('identity.tenant')}</Label><Input id="provider-tenant" bind:value={draft.microsoft_tenant_id} placeholder="common" /><p class="text-xs leading-relaxed text-muted-foreground">{$t('identity.tenantHint')}</p></div>{/if}
    {#if draft.kind==='oidc'}<div class="space-y-2"><Label for="provider-issuer">{$t('identity.issuer')}</Label><Input id="provider-issuer" type="url" bind:value={draft.issuer_url} required placeholder="https://id.company.com" /><p class="text-xs text-muted-foreground">{$t('identity.issuerHint')}</p></div>{/if}
    <div class="space-y-2"><Label for="provider-client">{$t('identity.clientID')}</Label><Input id="provider-client" bind:value={draft.client_id} required autocomplete="off" /></div>
    <div class="space-y-2"><Label for="provider-secret">{$t('identity.secret')}</Label><Input id="provider-secret" type="password" autocomplete="new-password" bind:value={secret} required={!draft.client_secret_set} placeholder={draft.client_secret_set?$t('identity.keepSecret'):''} /></div>
    <section class="space-y-3 rounded-lg border bg-muted/20 p-3" aria-label={$t('identity.redirects')}>
     <p class="text-xs font-medium">{$t('identity.redirects')}</p>
     {#if hasSSO}
      <div class="space-y-1.5"><p class="text-xs text-muted-foreground">{$t('identity.ssoCallback')}</p>
       {#if draft.id && origin}<div class="flex min-w-0 items-center gap-2"><code class="min-w-0 flex-1 break-all rounded-md border bg-background p-2 text-xs">{callback}</code><Button type="button" variant="outline" size="sm" onclick={()=>copyCallback(callback)} aria-label={$t('identity.copySSOCallback')}>{$t('common.copy')}</Button></div>
       {:else}<p class="text-xs leading-relaxed text-muted-foreground">{$t('identity.ssoCallbackAfterSave')}</p>{/if}
      </div>
     {/if}
     {#if hasCalendar}<div class="space-y-1.5"><p class="text-xs text-muted-foreground">{$t('identity.calendarCallback')}</p>
      {#if origin}<div class="flex min-w-0 items-center gap-2"><code class="min-w-0 flex-1 break-all rounded-md border bg-background p-2 text-xs">{calendarCallback}</code><Button type="button" variant="outline" size="sm" onclick={()=>copyCallback(calendarCallback)} aria-label={$t('identity.copyCalendarCallback')}>{$t('common.copy')}</Button></div>{/if}
     </div>{/if}
    </section>
    <div class="flex items-center justify-between gap-4"><Label for="provider-enabled">{$t('identity.enabled')}</Label><Switch id="provider-enabled" bind:checked={draft.enabled} /></div>
    {#if hasSSO}<section class="space-y-4 border-t pt-4"><h3 class="text-sm font-semibold">{$t('identity.access')}</h3><div class="flex items-center justify-between gap-4"><Label for="provider-members">{$t('identity.allowMembers')}</Label><Switch id="provider-members" bind:checked={draft.allow_members} /></div><div class="flex items-start justify-between gap-4"><div class="space-y-1"><Label for="provider-enrollment">{$t('identity.auto')}</Label><p class="text-xs leading-relaxed text-muted-foreground">{$t('identity.autoHint')}</p></div><Switch id="provider-enrollment" bind:checked={draft.auto_provision} /></div>
     {#if draft.auto_provision||draft.approved_domains.length||draft.group_ids.length}
     <ProviderValues id="provider-domains" label={$t('securitySettings.domains')} hint={$t('identity.domainsHint')} bind:values={draft.approved_domains} />
     {#if draft.kind!=='google'}<ProviderValues id="provider-groups" label={$t(draft.kind==='microsoft'?'identity.groups':'identity.oidcGroups')} hint={$t('identity.groupsHint')} bind:values={draft.group_ids} />
      {#if draft.group_ids.length>1}<div class="space-y-2"><Label for="provider-match">{$t('identity.match')}</Label><Select.Root type="single" bind:value={draft.group_match}><Select.Trigger id="provider-match" class="w-auto min-w-48 justify-between">{$t(`identity.${draft.group_match}`)}</Select.Trigger><Select.Content class="min-w-(--bits-select-anchor-width)"><Select.Item value="any">{$t('identity.any')}</Select.Item><Select.Item value="all">{$t('identity.all')}</Select.Item></Select.Content></Select.Root></div>{/if}
     {/if}
     {/if}
     <p class="rounded-lg bg-muted/30 p-3 text-xs leading-relaxed text-muted-foreground">{$t('identity.inviteHint')}</p>
    </section>{/if}
    <details class="rounded-lg border p-3"><summary class="cursor-pointer text-sm font-medium">{$t('identity.advanced')}</summary><div class="mt-4 space-y-4"><div class="flex items-center justify-between gap-4"><div class="space-y-1"><Label for="provider-pkce">PKCE (S256)</Label><p class="text-xs leading-relaxed text-muted-foreground">{$t('identity.pkceHint')}</p></div><Switch id="provider-pkce" bind:checked={draft.pkce} /></div>{#if draft.kind==='oidc'}<div class="space-y-2"><Label for="provider-claim">{$t('identity.groupsClaim')}</Label><Input id="provider-claim" bind:value={draft.groups_claim} /></div>{/if}</div></details>
   {:else}
    <div class="rounded-lg border bg-muted/20 p-4 text-sm"><p class="font-medium">{draft.name}</p><p class="mt-1 text-muted-foreground">{purposes.find(p=>p.value===draft.purpose)?.label} · {$t(draft.enabled?'identity.enabled':'identity.disabled')}</p><p class="mt-3 text-xs leading-relaxed text-muted-foreground">{$t('identity.changeHint')}</p></div>
    <VerifyIdentity />
   {/if}
   {#if error}<p role="alert" class="text-sm text-destructive">{error}</p>{/if}
   <Dialog.Footer><Button type="button" variant="outline" disabled={busy} onclick={()=>{if(review)review=false;else open=false;}}>{$t(review?'common.back':'common.cancel')}</Button><Button type="submit" disabled={busy}>{$t(busy?'common.saving':review?'common.save':'identity.review')}</Button></Dialog.Footer>
  </form>
 </Dialog.Content>
</Dialog.Root>
