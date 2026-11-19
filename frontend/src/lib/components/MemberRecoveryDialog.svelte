<script lang="ts">
 import PasswordPolicyHint from '$lib/components/PasswordPolicyHint.svelte';
 import * as Dialog from '$lib/components/ui/dialog';import { Button } from '$lib/components/ui/button';import { Input } from '$lib/components/ui/input';import { Label } from '$lib/components/ui/label';import { Switch } from '$lib/components/ui/switch';import VerifyIdentity from '$lib/components/VerifyIdentity.svelte';import { api } from '$lib/api';import { t } from '$lib/i18n';
 let {open=$bindable(false),userID='',passwordMode=false}:{open:boolean;userID:string;passwordMode?:boolean}=$props();
 let password=$state(''),reason=$state(''),disable=$state(false),error=$state(''),busy=$state(false),url=$state('');
 $effect(()=>{if(open){password='';reason='';disable=false;error='';url='';}});
 async function submit(){busy=true;error='';try{if(passwordMode){await api.post(`/v1/users/${userID}/password`,{password});open=false;}else{const result=await api.post<{recovery_url:string}>(`/v1/users/${userID}/mfa/reset`,{reason,disable});url=result.recovery_url;if(!url)open=false;}}catch(e){error=e instanceof Error?e.message:$t('nativeAuth.failed');}finally{busy=false;}}
</script>
<Dialog.Root bind:open><Dialog.Content class="max-h-[90dvh] overflow-y-auto sm:max-w-lg"><Dialog.Header><Dialog.Title>{$t(passwordMode?'nativeAuth.passwordRecovery':'nativeAuth.recoverMember')}</Dialog.Title><Dialog.Description>{$t(passwordMode?'nativeAuth.passwordHint':'nativeAuth.recoverMemberHint')}</Dialog.Description></Dialog.Header>
 {#if url}<p class="text-sm text-muted-foreground">{$t('nativeAuth.deliverLink')}</p><Input aria-label={$t('nativeAuth.recoverMember')} value={url} readonly /><Button type="button" onclick={()=>navigator.clipboard.writeText(url)}>{$t('common.copy')}</Button>
 {:else}<VerifyIdentity />
  {#if passwordMode}<PasswordPolicyHint /><div class="space-y-2"><Label for="member-new-password">{$t('nativeAuth.newPassword')}</Label><Input id="member-new-password" type="password" autocomplete="new-password" minlength={15} maxlength={128} bind:value={password} /></div>
  {:else}<div class="space-y-2"><Label for="mfa-recovery-reference">{$t('nativeAuth.ticket')}</Label><Input id="mfa-recovery-reference" maxlength={500} bind:value={reason} /></div><div class="flex items-center justify-between gap-4"><Label for="disable-optional-mfa">{$t('nativeAuth.disableOptional')}</Label><Switch id="disable-optional-mfa" bind:checked={disable} /></div>{/if}
  {#if error}<p role="alert" class="text-sm text-destructive">{error}</p>{/if}
  <Dialog.Footer><Button type="button" variant="outline" onclick={()=>open=false}>{$t('common.cancel')}</Button><Button type="button" disabled={busy||(!passwordMode&&reason.trim().length<3)|| (passwordMode&&password.length<8)} onclick={submit}>{$t(passwordMode?'members.setPassword':'nativeAuth.authorizeRecovery')}</Button></Dialog.Footer>
 {/if}
</Dialog.Content></Dialog.Root>
