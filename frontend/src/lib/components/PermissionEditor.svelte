<script lang="ts">
  import { onMount } from 'svelte';
  import { api, type PermissionGrant, type PermissionDefinition } from '$lib/api';
  import { currentUser } from '$lib/stores';
  import { t } from '$lib/i18n';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import * as Select from '$lib/components/ui/select';

  let { value = $bindable({}), delegated = false, scrollable = true }: { value: PermissionGrant; delegated?: boolean; scrollable?: boolean } = $props();
  let catalog = $state<PermissionDefinition[]>([]);
  let search = $state('');
  let starting=$state('');
  let defaults=$state<Record<string,PermissionGrant>>({});
  let error = $state('');
  const rank: Record<string,number> = { own:1, team:2, all:3 };
  const allowed = $derived(catalog.filter(p => (delegated || p.group!=='profile') && (!delegated || p.delegable) && !!$currentUser?.permissions?.[p.id]));
  const groups = $derived([...new Set(allowed.map(p => p.group))]);
  function options(p: PermissionDefinition) { return p.scopes.filter(s => rank[s] <= rank[$currentUser?.permissions?.[p.id] || '']); }
  function set(id: string, scope: string) { const next = {...value}; if(scope === 'none') delete next[id]; else next[id] = scope as 'own'|'team'|'all'; value = next; }
  function preset(kind: string) {
    const templates:Record<string,string[]>={assistant:['event_types.read','availability.read','bookings.read','bookings.create','bookings.reschedule','mcp.connect','mcp.manage'],reporting:['event_types.read','bookings.read'],invitations:['members.invite_member'],analyst:['bookings.read','notes.read','transcripts.read','mcp.connect','mcp.manage'],coordinator:['event_types.read','availability.read','bookings.read','bookings.create','bookings.reschedule','bookings.cancel','mcp.connect','mcp.manage'],concierge:['event_types.read','event_types.links','mcp.connect','mcp.manage']};
    const next: PermissionGrant = {};
    for (const p of allowed) {
      const scope=defaults[kind]?.[p.id];
      if(scope)next[p.id]=(options(p).includes(scope)?scope:options(p).at(-1)) as 'own'|'team'|'all';
      else if(templates[kind]?.includes(p.id))next[p.id]=options(p)[0] as 'own'|'team'|'all';
    }
    value = next;
  }
  onMount(async () => { try {const result=await api.get<{items:PermissionDefinition[];presets?:Record<string,PermissionGrant>}>('/v1/permissions');catalog=result.items??[];defaults=result.presets??{};} catch {error = $t('permissions.loadError');} });
</script>

<div class="space-y-4">
  <div class="flex flex-wrap items-center gap-2">
    <Select.Root type="single" bind:value={starting} onValueChange={preset}><Select.Trigger aria-label={$t('permissions.startFrom')} class="w-64 rounded-lg">{starting ? $t(`permissions.presets.${starting}`) : $t('permissions.startFrom')}</Select.Trigger><Select.Content>{#each ['member','admin','assistant','reporting','invitations','analyst','coordinator','concierge'] as kind}<Select.Item value={kind}>{$t(`permissions.presets.${kind}`)}</Select.Item>{/each}</Select.Content></Select.Root>
    <Button type="button" variant="ghost" size="sm" onclick={() => {value={};starting='';}}>{$t('permissions.clear')}</Button>
  </div>
  <p class="text-xs leading-relaxed text-muted-foreground">{$t(delegated ? 'permissions.credentialHint' : 'permissions.roleHint')}</p>
  <Input type="search" bind:value={search} placeholder={$t('permissions.search')} aria-label={$t('permissions.search')} />
  {#if error}<p role="alert" class="text-sm text-destructive">{error}</p>{/if}
  <div class={scrollable ? "max-h-80 space-y-4 overflow-y-auto pr-1" : "space-y-4"}>
    {#each groups as group}
      {@const entries = allowed.filter(p => p.group === group && `${$t(`permissions.groups.${p.group}`)} ${$t(`permissions.labels.${p.id}`)}`.toLowerCase().includes(search.toLowerCase()))}
      {#if entries.length}
        <fieldset class="space-y-2 rounded-lg border p-3">
          <legend class="px-1 text-xs font-semibold">{$t(`permissions.groups.${group}`)}</legend>
          {#each entries as p}
            <div class="flex items-center justify-between gap-3">
              <span id={`permission-${p.id}`} class="text-xs">{$t(`permissions.labels.${p.id}`)}</span>
              <Select.Root type="single" value={value[p.id] || 'none'} onValueChange={scope => set(p.id, scope)}>
                <Select.Trigger aria-labelledby={`permission-${p.id}`} class="h-8 w-40 shrink-0 rounded-lg text-xs">{$t(`permissions.scope.${value[p.id] || 'none'}`)}</Select.Trigger>
                <Select.Content class="min-w-[var(--bits-select-anchor-width)]">
                  <Select.Item value="none">{$t('permissions.scope.none')}</Select.Item>
                  {#each options(p) as scope}<Select.Item value={scope}>{$t(`permissions.scope.${scope}`)}</Select.Item>{/each}
                </Select.Content>
              </Select.Root>
            </div>
          {/each}
        </fieldset>
      {/if}
    {/each}
  </div>
</div>
