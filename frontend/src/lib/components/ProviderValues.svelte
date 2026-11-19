<script lang="ts">
 import { Button } from '$lib/components/ui/button';
 import { Input } from '$lib/components/ui/input';
 import { Label } from '$lib/components/ui/label';
 import { t } from '$lib/i18n';
 let { id, label, hint, values=$bindable<string[]>([]), max=20 }: { id:string; label:string; hint:string; values?:string[]; max?:number }=$props();
 let draft=$state('');
 function add(){const incoming=draft.split(/[,\n]+/).map(v=>v.trim()).filter(Boolean);if(new Set([...values,...incoming]).size>max)return;values=[...new Set([...values,...incoming])];draft='';}
</script>
<div class="space-y-2">
 <Label for={id}>{label}</Label>
 <div class="flex gap-2"><Input {id} bind:value={draft} onkeydown={e=>{if(e.key==='Enter'){e.preventDefault();add();}}} /><Button type="button" variant="outline" disabled={!draft.trim()} onclick={add}>{$t('identity.addValue')}</Button></div>
 <p class="text-xs leading-relaxed text-muted-foreground">{hint}</p>
 {#if values.length}<div class="flex flex-wrap gap-1.5">{#each values as value}<Button type="button" variant="secondary" size="sm" class="h-auto max-w-full gap-2 break-all px-2 py-1 text-left text-xs whitespace-normal" aria-label={$t('identity.removeValue',{value})} onclick={()=>values=values.filter(v=>v!==value)}>{value}<span aria-hidden="true">×</span></Button>{/each}</div>{/if}
 {#if draft.trim()}<p class="text-xs text-muted-foreground">{$t('identity.addHint')}</p>{/if}
</div>
