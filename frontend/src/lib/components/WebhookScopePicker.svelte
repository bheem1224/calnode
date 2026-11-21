<script lang="ts">
 import { t } from '$lib/i18n';
 import { currentUser } from '$lib/stores';
 import { buttonVariants } from '$lib/components/ui/button';
 import { Input } from '$lib/components/ui/input';
 import { Checkbox } from '$lib/components/ui/checkbox';
 import { Badge } from '$lib/components/ui/badge';
 import * as Popover from '$lib/components/ui/popover';
 import type { WebhookScope } from '$lib/api';
 let {value=$bindable([]),items,id='wh-scope'}:{value:WebhookScope[];items:(WebhookScope&{name:string})[];id?:string}=$props();
 let query=$state('');
 const key=(s:WebhookScope)=>s.type+':'+(s.id??'');
 function label(s:WebhookScope&{name?:string}){return s.type==='instance'?$t('webhookScopes.instance'):s.type==='user'&&s.id===$currentUser?.id?$t('webhookScopes.mine'):$t(`webhookScopes.types.${s.type}`)+': '+(s.name??s.id);}
 const visible=$derived(items.filter(s=>label(s).toLocaleLowerCase().includes(query.toLocaleLowerCase())));
 function toggle(s:WebhookScope){value=value.some(v=>key(v)===key(s))?value.filter(v=>key(v)!==key(s)):[...value,{type:s.type,...(s.id?{id:s.id}:{})}];}
</script>
<div class="space-y-2">
 <Popover.Root><Popover.Trigger {id} class={buttonVariants({variant:'outline',size:'sm'})} aria-label={$t('webhookScopes.select')}>{$t('webhookScopes.selected',{count:value.length})}</Popover.Trigger><Popover.Content align="start" class="w-80 max-w-[calc(100vw-2rem)] p-2"><Input bind:value={query} aria-label={$t('webhookScopes.search')} placeholder={$t('webhookScopes.search')} class="mb-2 h-8" /><div class="max-h-64 space-y-1 overflow-y-auto">{#each visible as scope (key(scope))}<label class="flex cursor-pointer items-start gap-2 rounded-md p-2 text-sm hover:bg-muted"><Checkbox checked={value.some(s=>key(s)===key(scope))} onCheckedChange={()=>toggle(scope)} /><span class="min-w-0 break-words">{label(scope)}</span></label>{:else}<p class="p-3 text-xs text-muted-foreground">{$t('webhookScopes.empty')}</p>{/each}</div></Popover.Content></Popover.Root>
 <div class="flex flex-wrap gap-1.5">{#each value as scope (key(scope))}<Badge variant="secondary" class="max-w-full whitespace-normal break-words">{label(items.find(s=>key(s)===key(scope))??scope)}</Badge>{/each}</div>
 <p class="max-w-xl text-xs leading-relaxed text-muted-foreground">{$t('webhookScopes.hint')}</p>
</div>
