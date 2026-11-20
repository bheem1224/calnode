<script lang="ts">
 import { onMount } from 'svelte';
 import { api } from '$lib/api';
 import { t } from '$lib/i18n';
 import { Button } from '$lib/components/ui/button';
 import { Badge } from '$lib/components/ui/badge';
 import { ConfirmDialog } from '$lib/components/ui/confirm-dialog';
 type Change={id:string;status:string;expires_at:string;operations:{name:string;target?:string;values?:Record<string,unknown>;before?:unknown}[];results:unknown[]};
 let item=$state<Change|null>(null),error=$state(''),busy=$state(false),confirm=$state(false),id=$state('');
 async function load(){item=await api.get<Change>('/v1/mcp/change-requests/'+encodeURIComponent(id));}
 onMount(async()=>{id=new URL(window.location.href).searchParams.get('id')??'';try{await load();}catch(e){error=e instanceof Error?e.message:$t('common.failed');}});
 async function decide(approve:boolean){busy=true;error='';try{await api.post('/v1/mcp/change-requests/'+encodeURIComponent(id)+'/decision',{approve});await load();}catch(e){error=e instanceof Error?e.message:$t('common.failed');}finally{busy=false;}}
</script>
<svelte:head><title>{$t('agentReview.title')}</title></svelte:head>
<div class="mx-auto w-full max-w-4xl space-y-5">
 <a href="/admin/connections" class="text-sm text-muted-foreground hover:text-foreground">← {$t('connections.title')}</a>
 <header><h1 class="text-xl font-semibold tracking-tight">{$t('agentReview.title')}</h1><p class="mt-1 text-sm leading-relaxed text-muted-foreground">{$t('agentReview.reviewHint')}</p></header>
 {#if error}<p role="alert" class="rounded-lg border border-destructive/30 bg-destructive/10 p-3 text-sm text-destructive">{error}</p>{/if}
 {#if item}<div class="flex flex-wrap items-center gap-3"><Badge variant="outline">{$t(`agentReview.status.${item.status}`)}</Badge><span class="text-xs text-muted-foreground">{$t('agentReview.expires')} {new Date(item.expires_at).toLocaleString()}</span></div>
 {#each item.operations as op,i}<section class="min-w-0 space-y-3 rounded-xl border bg-card p-5"><h2 class="break-words text-sm font-semibold">{i+1}. {op.name} {op.target??''}</h2>
 <div class={"grid min-w-0 gap-4 "+(op.before!==undefined?"md:grid-cols-2":"")}>{#if op.before!==undefined}<div class="min-w-0"><h3 class="mb-2 text-xs font-medium text-muted-foreground">{$t('agentReview.before')}</h3><pre class="max-h-72 overflow-auto whitespace-pre-wrap break-all rounded-lg border bg-muted/30 p-3 text-xs">{JSON.stringify(op.before,null,2)}</pre></div>{/if}<div class="min-w-0"><h3 class="mb-2 text-xs font-medium text-muted-foreground">{$t('agentReview.proposed')}</h3><pre class="max-h-72 overflow-auto whitespace-pre-wrap break-all rounded-lg border bg-muted/30 p-3 text-xs">{JSON.stringify(op.values??{},null,2)}</pre></div></div></section>{/each}
 {#if item.results?.length}<section class="min-w-0 rounded-xl border bg-card p-5"><h2 class="mb-3 text-sm font-semibold">{$t('agentReview.results')}</h2><pre class="max-h-96 overflow-auto whitespace-pre-wrap break-all text-xs">{JSON.stringify(item.results,null,2)}</pre></section>{/if}
 {#if item.status==='pending'}<p class="text-xs leading-relaxed text-muted-foreground">{$t('agentReview.batchHint')}</p><div class="flex gap-3"><Button type="button" variant="outline" disabled={busy} onclick={()=>decide(false)}>{$t('agentReview.reject')}</Button><Button type="button" disabled={busy||new Date(item.expires_at).getTime()<Date.now()} onclick={()=>confirm=true}>{$t('agentReview.approve')}</Button></div>{/if}
 {/if}
</div>
<ConfirmDialog bind:open={confirm} title={$t('agentReview.approve')} description={$t('agentReview.confirmHint')} onConfirm={()=>decide(true)} />
