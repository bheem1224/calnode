<script lang="ts">
  import { Calendar } from '$lib/components/ui/calendar';
  import * as Dialog from '$lib/components/ui/dialog';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { Badge } from '$lib/components/ui/badge';
  import { today, getLocalTimeZone, type DateValue } from '@internationalized/date';
  import { api, type EventType, type SingleUseLink } from '$lib/api';
  import { t } from '$lib/i18n';
  import { toast } from 'svelte-sonner';
  let { open = $bindable(false), event }: { open: boolean; event: EventType | null } = $props();
  let date = $state<DateValue | undefined>(today(getLocalTimeZone()));
  let clock = $state(''), slots = $state<{start_at: string; end_at: string}[]>([]), busy = $state(false), error = $state('');
  $effect(() => { if (open) { slots = []; clock = ''; error = ''; date = today(getLocalTimeZone()); } });
  function add() {
    if (!date || !clock || !event) return;
    const parts = clock.split(':').map(Number);
    const start = date.toDate(getLocalTimeZone()); start.setHours(parts[0], parts[1], 0, 0);
    if (!Number.isFinite(start.getTime()) || start.getHours() !== parts[0] || start.getMinutes() !== parts[1] || start <= new Date() || slots.length >= 20) { error = $t('customTimes.invalid'); return; }
    const start_at = start.toISOString(), end_at = new Date(start.getTime() + event.duration_minutes * 60000).toISOString();
    if (!slots.some(s => s.start_at === start_at)) slots = [...slots, {start_at, end_at}].sort((a,b) => a.start_at.localeCompare(b.start_at));
    error = '';
  }
  async function submit() {
    if (!event || !slots.length || busy) return; busy = true;
    try { const link = await api.post<SingleUseLink>(`/v1/event-types/${event.id}/single-use-links`, { custom_slots: slots }); await navigator.clipboard.writeText(`${window.location.origin}/t/${link.token}`); toast.success($t('customTimes.copied')); open = false; }
    catch { toast.error($t('customTimes.failed')); } finally { busy = false; }
  }
</script>
<Dialog.Root bind:open>
  <Dialog.Content class="sm:max-w-xl">
    <Dialog.Header><Dialog.Title>{$t('customTimes.title')}</Dialog.Title><Dialog.Description>{$t('customTimes.description')}</Dialog.Description></Dialog.Header>
    <p class="text-sm font-medium">{event?.name}</p>
    <div class="flex flex-wrap gap-4"><Calendar bind:value={date} minValue={today(getLocalTimeZone())} /><div class="min-w-40 flex-1 space-y-3"><label for="custom-time" class="text-sm font-medium">{$t('customTimes.time')}</label><Input id="custom-time" type="time" bind:value={clock} /><Button variant="outline" disabled={!clock || slots.length >= 20} onclick={add}>{$t('customTimes.add')}</Button><p class="text-xs text-muted-foreground">{$t('customTimes.timezone')}: {getLocalTimeZone()}</p></div></div>
    {#if error}<p class="text-sm text-destructive" role="alert">{error}</p>{/if}
    <div class="flex max-h-32 flex-wrap gap-2 overflow-y-auto">{#each slots as slot (slot.start_at)}<Badge variant="secondary" class="gap-2">{new Date(slot.start_at).toLocaleString()} <Button type="button" variant="ghost" size="icon" class="size-5" aria-label={$t('customTimes.remove')} onclick={() => slots = slots.filter(s => s.start_at !== slot.start_at)}>&times;</Button></Badge>{/each}</div>
    <Dialog.Footer><Button disabled={!slots.length || busy} onclick={submit}>{$t('customTimes.create')}</Button></Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>
