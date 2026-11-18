<script lang="ts">
  import { untrack } from 'svelte';
  import { api } from '$lib/api';
  import { currentUser } from '$lib/stores';
  import * as Dialog from '$lib/components/ui/dialog';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { Checkbox } from '$lib/components/ui/checkbox';
  import { t, tr } from '$lib/i18n';
  import { toast } from 'svelte-sonner';
  let { open = $bindable(false) }: { open: boolean } = $props();
  let users = $state<{ id: string; name: string; handle: string }[]>([]), query = $state(''), selected = $state<string[]>([]), error = $state('');
  const filtered = $derived(users.filter(u => `${u.name} ${u.handle}`.toLowerCase().includes(query.trim().toLowerCase())));
  const path = $derived(selected.length >= 2 ? `/u/${selected.join('+')}` : '');
  $effect(() => { if (open) untrack(() => { query = ''; selected = []; error = ''; api.get<{ items: { id: string; name: string; handle: string }[] }>('/v1/users/directory').then(result => { const me = $currentUser; users = [...(me ? [{ id: me.id, name: me.name, handle: me.handle || '' }] : []), ...result.items.filter(u => u.id !== me?.id)]; }).catch(() => { users = []; error = tr('dynamicLink.loadError'); }); }); });
  async function copy() { try { await navigator.clipboard.writeText(`${window.location.origin}${path}`); toast.success(tr('dynamicLink.copied')); } catch { toast.error(tr('dynamicLink.copyError')); } }
</script>
<Dialog.Root bind:open>
  <Dialog.Content class="max-w-md">
    <Dialog.Header><Dialog.Title>{$t('dynamicLink.title')}</Dialog.Title><Dialog.Description>{$t('dynamicLink.description')}</Dialog.Description></Dialog.Header>
    <Input bind:value={query} placeholder={$t('dynamicLink.search')} aria-label={$t('dynamicLink.search')} />
    <div class="max-h-56 divide-y overflow-y-auto rounded-lg border">
      {#if error}<p class="p-4 text-sm text-destructive">{error}</p>{:else if !filtered.length}<p class="p-4 text-sm text-muted-foreground">{$t('dynamicLink.empty')}</p>{/if}
      {#each filtered as u (u.id)}
        {@const handle = u.handle || `id:${u.id}`}
        <label class="flex cursor-pointer items-center gap-3 p-3 hover:bg-accent/50"><Checkbox checked={selected.includes(handle)} onCheckedChange={(checked) => { selected = checked ? [...selected, handle] : selected.filter(x => x !== handle); }} /><span class="min-w-0"><span class="block truncate text-sm font-medium">{u.name}</span><span class="block truncate text-xs text-muted-foreground">{u.handle ? `@${u.handle}` : $t('dynamicLink.noHandle')}</span></span></label>
      {/each}
    </div>
    {#if path}<p class="truncate rounded bg-muted px-2.5 py-1.5 font-mono text-xs">{path}</p>{:else}<p class="text-xs text-muted-foreground">{$t('dynamicLink.minimum')}</p>{/if}
    <Dialog.Footer><Button type="button" variant="ghost" onclick={() => open = false}>{$t('common.close')}</Button><Button type="button" variant="outline" disabled={!path} onclick={copy}>{$t('dynamicLink.copy')}</Button><Button type="button" disabled={!path} onclick={() => { window.open(path, '_blank', 'noopener,noreferrer'); open = false; }}>{$t('dynamicLink.open')}</Button></Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>
