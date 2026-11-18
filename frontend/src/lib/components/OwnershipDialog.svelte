<script lang="ts">
 import * as Dialog from '$lib/components/ui/dialog';
 import { Button } from '$lib/components/ui/button';
 import { Input } from '$lib/components/ui/input';
 import { Label } from '$lib/components/ui/label';
 import { api, type User } from '$lib/api';
 import { currentUser } from '$lib/stores';
 import { t } from '$lib/i18n';
 let { open=$bindable(false), target='', onDone }: {open:boolean;target?:string;onDone?:()=>void}=$props();
 let password=$state(''),error=$state(''),busy=$state(false);
 $effect(()=>{if(open){password='';error='';}});
 async function submit(){busy=true;error='';try{
  await api.post('/v1/auth/reauthenticate',{password});
  await api.post(target ? `/v1/users/${target}/transfer-ownership` : '/v1/users/me/claim-ownership',{});
  currentUser.set(await api.get<User>('/v1/users/me'));open=false;onDone?.();
 }catch(e:any){error=e.message;}finally{busy=false;}}
</script>
<Dialog.Root bind:open>
 <Dialog.Content class="sm:max-w-md">
  <Dialog.Header><Dialog.Title>{$t(target?'permissions.ownership.transfer':'permissions.ownership.claim')}</Dialog.Title><Dialog.Description>{$t('permissions.ownership.verify')}</Dialog.Description></Dialog.Header>
  <div class="space-y-2"><Label for="ownership-password">{$t('permissions.ownership.password')}</Label><Input id="ownership-password" type="password" autocomplete="current-password" bind:value={password} onkeydown={e=>{if(e.key==='Enter')submit();}} /></div>
  {#if error}<p role="alert" class="text-sm text-destructive">{error}</p>{/if}
  <Dialog.Footer><Button type="button" variant="outline" onclick={()=>open=false}>{$t('common.cancel')}</Button><Button type="button" disabled={busy} onclick={submit}>{$t('permissions.ownership.confirm')}</Button></Dialog.Footer>
 </Dialog.Content>
</Dialog.Root>
