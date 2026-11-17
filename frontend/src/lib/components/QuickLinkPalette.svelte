<script lang="ts">
  import * as Dialog from '$lib/components/ui/dialog';
  import { Input } from '$lib/components/ui/input';
  import { Button } from '$lib/components/ui/button';
  import { Badge } from '$lib/components/ui/badge';
  import { api, type EventType, type SingleUseLink } from '$lib/api';
  import { t } from '$lib/i18n';
  import { toast } from 'svelte-sonner';
  import OfferCustomTimes from '$lib/components/OfferCustomTimes.svelte';
  import DynamicLinkDialog from '$lib/components/DynamicLinkDialog.svelte';
  let dynamicOpen = $state(false);
  let customOpen = $state(false), customEvent = $state<EventType | null>(null);
  let { open = $bindable(false) }: { open: boolean } = $props();
  let items = $state<EventType[]>([]), query = $state(''), busy = $state(false), selected = $state(0), error = $state('');
  function matches(text: string, search: string) {
    let i = 0; for (const c of text.toLowerCase()) if (c === search[i]) i++;
    return i === search.length;
  }
  const visible = $derived(items.filter(x => matches(`${x.name} ${x.slug}`, query.trim().toLowerCase())));
  $effect(() => { if (open) { query = ''; selected = 0; error = ''; api.get<{items: EventType[]}>('/v1/event-types').then(r => items = r.items.filter(x => x.is_active && !x.archived)).catch(() => error = $t('quickLink.loadError')); } });
  async function copy(item: EventType, once = false) {
    if (busy) return; busy = true;
    try {
      const url = once ? `${window.location.origin}/t/${(await api.post<SingleUseLink>(`/v1/event-types/${item.id}/single-use-links`, {})).token}` : `${window.location.origin}/book/${item.slug}`;
      await navigator.clipboard.writeText(url); toast.success($t('quickLink.copied')); open = false;
    } catch { toast.error($t('quickLink.copyError')); } finally { busy = false; }
  }
  function keys(e: KeyboardEvent) {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') { e.preventDefault(); selected = Math.max(0, Math.min(visible.length - 1, selected + (e.key === 'ArrowDown' ? 1 : -1))); }
    if (e.key === 'Enter' && visible[selected]) { e.preventDefault(); void copy(visible[selected], e.shiftKey); }
  }
</script>
<DynamicLinkDialog bind:open={dynamicOpen} />
<OfferCustomTimes bind:open={customOpen} event={customEvent} />
<Dialog.Root bind:open>
  <Dialog.Content class="max-h-[calc(100dvh-2rem)] w-[calc(100%-2rem)] overflow-y-auto sm:max-w-xl">
    <Dialog.Header><Dialog.Title>{$t('quickLink.title')}</Dialog.Title><Dialog.Description>{$t('quickLink.description')}</Dialog.Description></Dialog.Header>
    <Input aria-label={$t('quickLink.search')} placeholder={$t('quickLink.search')} bind:value={query} oninput={() => selected = 0} onkeydown={keys} />
    <Button type="button" variant="outline" class="justify-start" onclick={() => { open = false; dynamicOpen = true; }}>{$t('dynamicLink.title')}</Button>
    <div class="max-h-80 space-y-1 overflow-y-auto">
      {#if error}<p class="text-sm text-destructive">{error}</p>{:else if visible.length === 0}<p class="p-3 text-sm text-muted-foreground">{$t('quickLink.empty')}</p>{/if}
      {#each visible as item, i (item.id)}
        <div class="flex flex-wrap items-center gap-2 rounded-md p-2 {selected === i ? 'bg-muted' : ''}">
          <div class="min-w-0 flex-1 basis-32"><p class="truncate text-sm font-medium">{item.name}</p><p class="truncate text-xs text-muted-foreground">/{item.slug}</p></div>
          <Badge variant="secondary">{item.duration_minutes}m</Badge>
          <div class="flex w-full flex-wrap items-center gap-1 sm:w-auto">
            <Button type="button" size="sm" variant="outline" disabled={busy} onclick={() => copy(item)}>{$t('quickLink.copy')}</Button>
            <Button type="button" size="sm" variant="ghost" disabled={busy} onclick={() => { customEvent = item; open = false; customOpen = true; }}>{$t('customTimes.title')}</Button>
            <Button type="button" size="sm" variant="ghost" disabled={busy} onclick={() => copy(item, true)}>{$t('quickLink.once')}</Button>
          </div>
        </div>
      {/each}
    </div>
  </Dialog.Content>
</Dialog.Root>
