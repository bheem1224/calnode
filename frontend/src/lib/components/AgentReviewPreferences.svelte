<script lang="ts">
 import { onMount } from 'svelte';
 import { api } from '$lib/api';
 import { t } from '$lib/i18n';
 import { Button } from '$lib/components/ui/button';
 import { Label } from '$lib/components/ui/label';
 import { Switch } from '$lib/components/ui/switch';
 import VerifyIdentity from '$lib/components/VerifyIdentity.svelte';
 import { toast } from 'svelte-sonner';
 type Preferences={review_required:boolean;enforced:boolean;effective_review_required:boolean};
 let data=$state<Preferences|null>(null),required=$state(true),busy=$state(false),error=$state('');
 let requests=$state<{id:string;status:string;created_at:string;expires_at:string}[]>([]);
 onMount(async()=>{try{data=await api.get<Preferences>('/v1/users/me/mcp-preferences');required=data.enforced||data.review_required;requests=(await api.get<{items:typeof requests}>('/v1/mcp/change-requests')).items;}catch(e){error=e instanceof Error?e.message:$t('common.failed');}});
 async function save(){busy=true;error='';try{data=await api.put<Preferences>('/v1/users/me/mcp-preferences',{review_required:required});required=data.enforced||data.review_required;toast.success($t('agentReview.saved'));}catch(e){error=e instanceof Error?e.message:$t('common.failed');}finally{busy=false;}}
</script>
<section class="mb-6 max-w-3xl space-y-4 rounded-xl border bg-card p-5">
 <div><h2 class="text-sm font-semibold">{$t('agentReview.preferencesTitle')}</h2><p class="mt-1 text-xs leading-relaxed text-muted-foreground">{$t('agentReview.preferencesHint')}</p></div>
 {#if error}<p role="alert" class="text-sm text-destructive">{error}</p>{/if}
 {#if data}<div class="flex items-start justify-between gap-6"><div class="space-y-1"><Label for="agent-review-personal">{$t('agentReview.personalTitle')}</Label><p class="text-xs text-muted-foreground">{$t(data.enforced?'agentReview.enforced':'agentReview.personalHint')}</p></div><Switch id="agent-review-personal" bind:checked={required} disabled={data.enforced||busy} /></div>
 {#if !required && data.review_required}<VerifyIdentity />{/if}
 <Button type="button" size="sm" variant="outline" disabled={busy||data.enforced||required===data.review_required} onclick={save}>{$t('common.save')}</Button>{/if}
 {#if requests.length}<div class="space-y-2 border-t pt-4"><h3 class="text-xs font-medium">{$t('agentReview.recent')}</h3>{#each requests as request}<a class="flex items-center justify-between gap-3 rounded-lg border p-3 text-sm hover:bg-muted/40" href={'/admin/connections/review?id='+encodeURIComponent(request.id)}><span class="min-w-0 truncate font-mono text-xs">{request.id}</span><span class="text-xs text-muted-foreground">{$t(`agentReview.status.${request.status}`)}</span></a>{/each}</div>{/if}
</section>
