<script lang="ts">
 import HelpTooltip from '$lib/components/HelpTooltip.svelte';
 import { onMount } from 'svelte';
 import { api } from '$lib/api';
 import { t } from '$lib/i18n';
 import { Button } from '$lib/components/ui/button';
 import * as Dialog from '$lib/components/ui/dialog';
 import VerifyIdentity from './VerifyIdentity.svelte';
 type Health={failed_jobs:{id:string;type:string;error:string}[];provider_work?:{id:string;type:string;error:string;booking_id?:string;owner?:string}[];calendar_errors:{id:string;name:string;error:string;checked_at:string}[];failed_confirmations:number;pending_jobs:number};
 let health=$state<Health|null>(null),error=$state(''),selected=$state(''),open=$state(false),busy=$state(false);
 async function refresh(){try{health=await api.get<Health>('/v1/settings/security/operations');error='';}catch(e){error=e instanceof Error?e.message:$t('securityOperations.loadError');}}
 onMount(()=>{void refresh();});
 async function retry(){busy=true;try{await api.post('/v1/settings/security/jobs/'+encodeURIComponent(selected)+'/retry',{});open=false;await refresh();}catch(e){error=e instanceof Error?e.message:$t('securityOperations.retryError');}finally{busy=false;}}
</script>
<section class="space-y-4 rounded-xl border bg-card p-5" aria-labelledby="security-operations">
 <div class="flex flex-wrap items-center justify-between gap-3"><div><div class="flex items-center gap-1"><h3 id="security-operations" class="text-sm font-semibold">{$t('securityOperations.title')}</h3><HelpTooltip label={$t('securityOperations.title')} text={$t('securityOperations.description')} /></div></div><Button type="button" size="sm" variant="outline" onclick={refresh}>{$t('securityOperations.refresh')}</Button></div>
 {#if error}<p role="alert" class="break-words text-sm text-destructive">{error}</p>{/if}
 {#if health}
  <p class="text-xs text-muted-foreground">{$t('securityOperations.counts',{pending:health.pending_jobs,failed:health.failed_confirmations})}</p>
  {#if !health.calendar_errors.length && !health.failed_jobs.length && !health.provider_work?.length}<p class="rounded-lg bg-muted/30 p-3 text-sm">{$t('securityOperations.healthy')}</p>{/if}
  {#each health.calendar_errors as account}<div class="space-y-1 rounded-lg border p-3"><p class="text-sm font-medium">{account.name}</p><p class="text-xs text-muted-foreground">{$t('securityOperations.calendarError')}</p></div>{/each}
  {#each health.failed_jobs as job}<div class="flex flex-wrap items-center justify-between gap-3 rounded-lg border p-3"><div class="min-w-0 flex-1"><p class="break-words text-sm font-medium">{job.type}</p><p class="mt-1 break-all text-xs text-muted-foreground">{job.error}</p></div><Button type="button" variant="outline" size="sm" onclick={()=>{selected=job.id;error='';open=true;}}>{$t('securityOperations.retry')}</Button></div>{/each}
  {#each health.provider_work??[] as job}<div class="flex flex-col items-stretch gap-3 rounded-lg border p-3 sm:flex-row sm:items-center sm:justify-between"><div class="min-w-0 flex-1"><p class="break-words text-sm font-medium">{$t(job.type==='calendar.cleanup'?'securityOperations.calendarCleanup':'securityOperations.zoomRecovery')}</p>{#if job.owner}<p class="mt-1 break-words text-xs text-muted-foreground">{job.owner}</p>{/if}{#if job.booking_id}<p class="mt-1 break-all text-xs text-muted-foreground">{$t('securityOperations.bookingReference')}: {job.booking_id}</p>{/if}<p class="mt-1 break-words text-xs text-muted-foreground">{job.error}</p></div><Button type="button" class="min-h-11 w-full sm:w-auto" variant="outline" size="sm" onclick={()=>{selected=job.id;error='';open=true;}}>{$t('securityOperations.retry')}</Button></div>{/each}
 {/if}
</section>
<Dialog.Root bind:open><Dialog.Content class="sm:max-w-md"><Dialog.Header><Dialog.Title>{$t('securityOperations.retry')}</Dialog.Title><Dialog.Description>{$t('securityOperations.retryHint')}</Dialog.Description></Dialog.Header><VerifyIdentity />{#if error}<p role="alert" class="break-words text-sm text-destructive">{error}</p>{/if}<Dialog.Footer><Button type="button" variant="outline" onclick={()=>open=false}>{$t('common.cancel')}</Button><Button type="button" disabled={busy} onclick={retry}>{$t('securityOperations.retry')}</Button></Dialog.Footer></Dialog.Content></Dialog.Root>
