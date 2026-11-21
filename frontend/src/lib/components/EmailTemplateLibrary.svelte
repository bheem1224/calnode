<script lang="ts">
 import { onMount } from 'svelte';
 import { api, type EmailTemplate } from '$lib/api';
 import { t } from '$lib/i18n';
 import { Button, buttonVariants } from '$lib/components/ui/button';
 import { Input } from '$lib/components/ui/input';
 import { Badge } from '$lib/components/ui/badge';
 import { ConfirmDialog } from '$lib/components/ui/confirm-dialog';
 import Pencil from '@lucide/svelte/icons/pencil';
 import Trash2 from '@lucide/svelte/icons/trash-2';
 import * as Tooltip from '$lib/components/ui/tooltip';
 import * as Popover from '$lib/components/ui/popover';
 import EmailTemplateModal from '$lib/components/event-types/EmailTemplateModal.svelte';
 type Kind='confirmation'|'cancellation'|'reschedule'|'reminder';
 const kinds:Kind[]=['confirmation','cancellation','reschedule','reminder'];
 let items=$state<EmailTemplate[]>([]),query=$state(''),error=$state(''),loading=$state(true),open=$state(false),removeOpen=$state(false);
 let selected=$state<EmailTemplate|null>(null),removing=$state<EmailTemplate|null>(null),kind=$state<Kind>('confirmation'),newOpen=$state(false);
 const filtered=$derived(items.filter(item=>(item.name+' '+item.subject+' '+$t(`templateLibrary.kinds.${item.kind}`)).toLocaleLowerCase().includes(query.trim().toLocaleLowerCase())));
 async function load(){try{items=(await api.get<{items:EmailTemplate[]}>('/v1/email-templates')).items;}catch(e){error=e instanceof Error?e.message:$t('common.failed');}finally{loading=false;}}
 onMount(load);
 function edit(item:EmailTemplate|null,next:Kind){selected=item;kind=next;open=true;}
 async function remove(){if(!removing)return;try{await api.del('/v1/email-templates/'+removing.id);await load();}catch(e){error=e instanceof Error?e.message:$t('common.failed');}}
</script>
<section class="mt-8 max-w-3xl space-y-4 border-t pt-6">
 <header class="flex flex-wrap items-start justify-between gap-3"><div><h2 class="text-base font-semibold">{$t('templateLibrary.title')}</h2><p class="mt-1 max-w-xl text-xs leading-relaxed text-muted-foreground">{$t('templateLibrary.hint')}</p></div>
 <Popover.Root bind:open={newOpen}><Popover.Trigger class={buttonVariants({size:'sm'})}>+ {$t('templateLibrary.create')}</Popover.Trigger><Popover.Content align="end" class="w-56 p-1">{#each kinds as next}<Button type="button" variant="ghost" size="sm" class="w-full justify-start" onclick={()=>{newOpen=false;edit(null,next);}}> {$t(`templateLibrary.kinds.${next}`)}</Button>{/each}</Popover.Content></Popover.Root></header>
 <Input bind:value={query} aria-label={$t('templateLibrary.search')} placeholder={$t('templateLibrary.search')} class="max-w-sm" />
 {#if error}<p role="alert" class="text-sm text-destructive">{error}</p>{/if}
 {#if loading}<p class="text-sm text-muted-foreground">{$t('common.loading')}</p>{:else}<div class="divide-y overflow-hidden rounded-xl border bg-card">{#each filtered as item (item.id)}<div class="flex flex-wrap items-center justify-between gap-3 p-4"><div class="min-w-0"><p class="break-words text-sm font-medium">{item.name}</p><div class="mt-1 flex flex-wrap items-center gap-2 text-xs text-muted-foreground"><span>{$t(`templateLibrary.kinds.${item.kind}`)}</span>{#if item.is_default}<Badge variant="secondary">{$t('templateLibrary.default')}</Badge>{/if}<span>{item.mode==='html'?'HTML':$t('templateLibrary.note')}</span></div></div><Tooltip.Provider><div class="flex gap-2"><Tooltip.Root><Tooltip.Trigger class={buttonVariants({variant:'ghost',size:'icon'})} aria-label={$t('common.edit')} onclick={()=>edit(item,item.kind as Kind)}><Pencil class="size-4" /></Tooltip.Trigger><Tooltip.Content>{$t('common.edit')}</Tooltip.Content></Tooltip.Root><Tooltip.Root><Tooltip.Trigger class={buttonVariants({variant:'ghost',size:'icon'})} aria-label={$t('common.delete')} onclick={()=>{removing=item;removeOpen=true;}}><Trash2 class="size-4 text-destructive" /></Tooltip.Trigger><Tooltip.Content>{$t('common.delete')}</Tooltip.Content></Tooltip.Root></div></Tooltip.Provider></div>{:else}<p class="p-6 text-center text-sm text-muted-foreground">{$t('templateLibrary.empty')}</p>{/each}</div>{/if}
</section>
<EmailTemplateModal bind:open libraryMode libraryTemplate={selected} {kind} subject={selected?.subject??''} noteBody={selected?.mode==='note'?selected.body:''} htmlBody={selected?.mode==='html'?selected.body:''} onLibrarySaved={load} />
<ConfirmDialog bind:open={removeOpen} title={$t('templateLibrary.deleteTitle')} description={$t('templateLibrary.deleteHint')} destructive onConfirm={remove} />
