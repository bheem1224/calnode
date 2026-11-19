<script lang="ts">
 import { onMount } from 'svelte';
 import LinkedIdentities from '$lib/components/LinkedIdentities.svelte';
 import { api } from '$lib/api';
 import { t } from '$lib/i18n';
 import { Button } from '$lib/components/ui/button';
 import { Input } from '$lib/components/ui/input';
 import { Label } from '$lib/components/ui/label';
 import { ConfirmDialog } from '$lib/components/ui/confirm-dialog';
 import { registerPasskey,signInWithPasskey } from '$lib/native-auth';
 import { toast } from 'svelte-sonner';
 type Status={totp_enabled:boolean;passkeys:{id:string;name:string;created_at:string}[];recovery_codes_remaining:number;setup_required:boolean;native_allowed:boolean;mfa_required:boolean};
 let removeOpen=$state(false);
 $effect(()=>{removeOpen=!!remove;});
 let status=$state<Status|null>(null),error=$state(''),busy=$state(false),password=$state(''),code=$state(''),keyName=$state(''),setup=$state<{secret:string;qr:string;setup_token:string}|null>(null),codes=$state<string[]>([]),remove=$state('');
 async function load(){status=await api.get<Status>('/v1/users/me/security');}
 onMount(()=>{load().catch(e=>error=e.message);});
 async function action(fn:()=>Promise<unknown>){busy=true;error='';try{await fn();await load();}catch(e){error=e instanceof Error?e.message:$t('nativeAuth.failed');}finally{busy=false;}}
 async function verify(passkey=false){await action(async()=>{if(passkey)await signInWithPasskey(true);else await api.post('/v1/auth/reauthenticate',{password,code});password='';code='';toast.success($t('nativeAuth.verified'));});}
 async function enrollKey(){await action(async()=>{await registerPasskey(keyName);keyName='';toast.success($t('nativeAuth.enrolled'));});}
 async function confirm(){await action(async()=>{await api.post('/v1/users/me/security/totp/confirm',{...setup,code});setup=null;code='';toast.success($t('nativeAuth.enrolled'));});}
</script>
<div class="w-full max-w-2xl space-y-5">
 <header><h2 class="text-xl font-semibold tracking-tight">{$t('nativeAuth.title')}</h2><p class="mt-1 text-sm text-muted-foreground">{$t('nativeAuth.description')}</p></header>
 {#if error}<p role="alert" class="rounded-lg bg-destructive/10 p-3 text-sm text-destructive">{error}</p>{/if}
 {#if status}
  {#if !status.setup_required}<LinkedIdentities />{/if}
  {#if status.setup_required}<div class="rounded-lg border bg-muted/40 p-4 text-sm">{$t('nativeAuth.setupRequired')}</div>{/if}
  {#if !status.native_allowed}<p class="text-sm text-muted-foreground">{$t('nativeAuth.ssoManaged')}</p>
  {:else}
   {#if !status.setup_required}
    <section class="space-y-3 rounded-xl border bg-card p-5"><h3 class="text-sm font-semibold">{$t('nativeAuth.verifyTitle')}</h3><p class="text-xs text-muted-foreground">{$t('nativeAuth.verifyHint')}</p>
     <div class="grid gap-3 sm:grid-cols-2"><div class="space-y-1.5"><Label for="auth-current-password">{$t('securitySettings.currentPassword')}</Label><Input id="auth-current-password" type="password" autocomplete="current-password" bind:value={password} /></div><div class="space-y-1.5"><Label for="auth-verify-code">{$t('nativeAuth.code')}</Label><Input id="auth-verify-code" autocomplete="one-time-code" bind:value={code} /></div></div>
     <div class="flex flex-wrap gap-2"><Button type="button" variant="outline" disabled={busy} onclick={()=>verify()}>{$t('nativeAuth.verify')}</Button><Button type="button" variant="outline" disabled={busy} onclick={()=>verify(true)}>{$t('nativeAuth.usePasskey')}</Button></div>
    </section>
   {/if}
   <section class="space-y-4 rounded-xl border bg-card p-5"><div><h3 class="text-sm font-semibold">{$t('nativeAuth.passkeys')}</h3><p class="mt-1 text-xs text-muted-foreground">{$t('nativeAuth.passkeyHint')}</p></div>
    {#each status.passkeys as key}<div class="flex items-center justify-between gap-3 rounded-lg border p-3"><span class="min-w-0 truncate text-sm">{key.name}</span><Button type="button" size="sm" variant="ghost" disabled={busy} onclick={()=>{remove=key.id;removeOpen=true;}}>{$t('common.remove')}</Button></div>{/each}
    <div class="flex flex-wrap items-end gap-2"><div class="min-w-0 flex-1 space-y-1.5"><Label for="passkey-name">{$t('nativeAuth.keyName')}</Label><Input id="passkey-name" maxlength={80} bind:value={keyName} /></div><Button type="button" disabled={busy} onclick={enrollKey}>{$t('nativeAuth.addPasskey')}</Button></div>
   </section>
   <section class="space-y-3 rounded-xl border bg-card p-5"><h3 class="text-sm font-semibold">{$t('nativeAuth.authenticator')}</h3><p class="text-xs text-muted-foreground">{$t('nativeAuth.totpHint')}</p>
    {#if status.totp_enabled}<div class="flex items-center justify-between gap-3"><span class="text-sm">{$t('nativeAuth.enabled')}</span><Button type="button" variant="outline" disabled={busy} onclick={()=>{remove='totp';removeOpen=true;}}>{$t('common.remove')}</Button></div>
    {:else if setup}<div class="space-y-3"><img class="h-48 w-48 rounded-lg bg-white p-2" src={setup.qr} alt={$t('nativeAuth.qrAlt')} /><p class="break-all rounded-lg bg-muted p-3 font-mono text-xs">{setup.secret}</p><Label for="totp-code">{$t('nativeAuth.code')}</Label><Input id="totp-code" inputmode="numeric" autocomplete="one-time-code" bind:value={code} /><Button type="button" disabled={busy} onclick={confirm}>{$t('nativeAuth.confirmAuthenticator')}</Button></div>
    {:else}<Button type="button" variant="outline" disabled={busy} onclick={()=>action(async()=>{setup=await api.post('/v1/users/me/security/totp/begin',{});})}>{$t('nativeAuth.addAuthenticator')}</Button>{/if}
   </section>
   {#if !status.setup_required && (status.passkeys.length||status.totp_enabled)}
    <section class="space-y-3 rounded-xl border bg-card p-5"><h3 class="text-sm font-semibold">{$t('nativeAuth.recoveryCodes')}</h3><p class="text-xs text-muted-foreground">{$t('nativeAuth.recoveryHint',{count:status.recovery_codes_remaining})}</p><Button type="button" variant="outline" disabled={busy} onclick={()=>action(async()=>{const result=await api.post<{codes:string[]}>('/v1/users/me/security/recovery-codes',{});codes=result.codes;})}>{$t('nativeAuth.generateCodes')}</Button>
     {#if codes.length}<p class="text-sm font-medium">{$t('nativeAuth.saveCodes')}</p><div class="grid gap-2 rounded-lg border bg-muted/30 p-3 sm:grid-cols-2">{#each codes as recovery}<code class="break-all text-xs">{recovery}</code>{/each}</div><Button type="button" variant="outline" onclick={async()=>{await navigator.clipboard.writeText(codes.join('\n'));toast.success($t('nativeAuth.copied'));}}>{$t('common.copy')}</Button>{/if}
    </section>
   {/if}
   {#if !status.setup_required}<Button type="button" onclick={()=>window.location.assign('/admin')}>{$t('nativeAuth.continue')}</Button>{/if}
  {/if}
 {:else}<p class="text-sm text-muted-foreground">{$t('securitySettings.loading')}</p>{/if}
</div>
<ConfirmDialog bind:open={removeOpen} title={$t('nativeAuth.removeTitle')} description={$t('nativeAuth.removeHint')} destructive onConfirm={async()=>{const id=remove;remove='';await action(()=>api.del('/v1/users/me/security/factors/'+encodeURIComponent(id)));}} />
