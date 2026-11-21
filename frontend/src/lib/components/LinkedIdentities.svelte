<script lang="ts">
 import HelpTooltip from '$lib/components/HelpTooltip.svelte';
 import { onMount } from 'svelte';
 import { api } from '$lib/api';
 import { t } from '$lib/i18n';
 import { Button } from '$lib/components/ui/button';
 import { Badge } from '$lib/components/ui/badge';
 import VerifyIdentity from '$lib/components/VerifyIdentity.svelte';
 import * as Dialog from '$lib/components/ui/dialog';
 let identities=$state<{id:string;name:string;linked:boolean}[]>([]),error=$state(''),selected=$state(''),open=$state(false);
 onMount(async()=>{try{const data=await api.get<{items:typeof identities}>('/v1/users/me/identities');identities=data.items??[];}catch(e){error=e instanceof Error?e.message:$t('common.failed');}});
</script>
{#if identities.length || error}<section class="space-y-3 rounded-xl border bg-card p-5"><div class="flex items-center gap-1"><h3 class="text-sm font-semibold">{$t('identity.linkedTitle')}</h3><HelpTooltip label={$t('identity.linkedTitle')} text={$t('identity.linkHint')} /></div>{#if error}<p role="alert" class="text-sm text-destructive">{error}</p>{/if}{#each identities as identity}<div class="flex items-center justify-between gap-3"><span class="min-w-0 break-words text-sm">{identity.name}</span>{#if identity.linked}<Badge variant="secondary">{$t('identity.linked')}</Badge>{:else}<Button type="button" variant="outline" size="sm" onclick={()=>{selected=identity.id;open=true;}}>{$t('identity.link')}</Button>{/if}</div>{/each}</section>{/if}
<Dialog.Root bind:open><Dialog.Content class="sm:max-w-md"><Dialog.Header><Dialog.Title>{$t('identity.link')}</Dialog.Title><Dialog.Description>{$t('identity.linkHint')}</Dialog.Description></Dialog.Header><VerifyIdentity /><Dialog.Footer><Button type="button" variant="outline" onclick={()=>open=false}>{$t('common.cancel')}</Button><Button type="button" onclick={()=>window.location.assign('/v1/auth/providers/'+encodeURIComponent(selected)+'/login?link=1')}>{$t('identity.continue')}</Button></Dialog.Footer></Dialog.Content></Dialog.Root>
