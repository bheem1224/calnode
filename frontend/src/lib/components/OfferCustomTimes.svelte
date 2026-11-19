<script lang="ts">
 import { singleUseLink } from '$lib/booking-links';
  import { Calendar } from '$lib/components/ui/calendar';
  import * as Dialog from '$lib/components/ui/dialog';
  import { Button } from '$lib/components/ui/button';
  import { Combobox } from '$lib/components/ui/combobox';
  import { Label } from '$lib/components/ui/label';
  import { Badge } from '$lib/components/ui/badge';
  import { today, getLocalTimeZone, type DateValue } from '@internationalized/date';
  import { api, type EventType, type SingleUseLink } from '$lib/api';
  import { t } from '$lib/i18n';
  import { toast } from 'svelte-sonner';
  let { open = $bindable(false), event }: { open: boolean; event: EventType | null } = $props();
  let date = $state<DateValue | undefined>(today(getLocalTimeZone()));
  let clock = $state(''), slots = $state<{start_at: string; end_at: string}[]>([]), busy = $state(false), error = $state('');
  let hour = $state(''), minute = $state('00');
  const hours = Array.from({length:24},(_,i)=>({value:String(i).padStart(2,'0'),label:String(i).padStart(2,'0')}));
  const minutes = Array.from({length:60},(_,i)=>({value:String(i).padStart(2,'0'),label:String(i).padStart(2,'0')}));
  $effect(()=>{clock=hour ? `${hour}:${minute}` : '';});
  $effect(() => { if (open) { slots = []; hour = ''; minute = '00'; error = ''; date = today(getLocalTimeZone()); } });
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
    try { const link = await api.post<SingleUseLink>(`/v1/event-types/${event.id}/single-use-links`, { custom_slots: slots }); await navigator.clipboard.writeText(singleUseLink(link)); toast.success($t('customTimes.copied')); open = false; }
    catch { toast.error($t('customTimes.failed')); } finally { busy = false; }
  }
</script>
<Dialog.Root bind:open>
  <Dialog.Content class="max-h-[90dvh] overflow-y-auto rounded-xl sm:max-w-2xl">
    <Dialog.Header><Dialog.Title>{$t('customTimes.title')}</Dialog.Title><Dialog.Description>{$t('customTimes.description')}</Dialog.Description></Dialog.Header>
    <div class="flex items-center justify-between gap-3 rounded-lg border bg-muted/25 px-3 py-2"><p class="text-sm font-medium">{event?.name}</p><Badge variant="outline">{event?.duration_minutes} min</Badge></div>
    <div class="grid items-start gap-5 sm:grid-cols-[auto_minmax(0,1fr)]">
      <div class="rounded-xl border bg-muted/15 p-2"><Calendar bind:value={date} minValue={today(getLocalTimeZone())} /></div>
      <div class="min-w-0 space-y-4 rounded-xl border p-4">
        <div><p class="text-sm font-medium">{$t('customTimes.time')}</p><p class="mt-1 text-xs text-muted-foreground">{date?.toDate(getLocalTimeZone()).toLocaleDateString(undefined,{weekday:'long',month:'short',day:'numeric'})}</p></div>
        <div class="grid grid-cols-[1fr_auto_1fr] items-end gap-2">
          <div class="min-w-0 space-y-1.5"><Label for="custom-hour">{$t('customTimes.hour')}</Label><Combobox id="custom-hour" items={hours} bind:value={hour} placeholder="--" class="h-9 rounded-lg" /></div>
          <span class="pb-2 text-muted-foreground">:</span>
          <div class="min-w-0 space-y-1.5"><Label for="custom-minute">{$t('customTimes.minute')}</Label><Combobox id="custom-minute" items={minutes} bind:value={minute} class="h-9 rounded-lg" /></div>
        </div>
        <p class="text-xs leading-relaxed text-muted-foreground">{$t('customTimes.timezone')}: <span class="break-words">{getLocalTimeZone()}</span><br />{$t('customTimes.clockHint')}</p>
        <Button type="button" variant="outline" class="w-full rounded-lg" disabled={!clock || slots.length >= 20} onclick={add}>{$t('customTimes.add')}</Button>
      </div>
    </div>
    {#if error}<p class="text-sm text-destructive" role="alert">{error}</p>{/if}
    <div class="flex max-h-36 flex-wrap gap-2 overflow-y-auto">{#each slots as slot (slot.start_at)}<Badge variant="secondary" class="gap-2 rounded-lg py-1 pl-3 pr-1">{new Date(slot.start_at).toLocaleString()} <Button type="button" variant="ghost" size="icon" class="size-6 rounded-md" aria-label={$t('customTimes.remove')} onclick={() => slots = slots.filter(s => s.start_at !== slot.start_at)}>&times;</Button></Badge>{/each}</div>
    <Dialog.Footer class="border-t pt-4"><Button type="button" class="rounded-lg" disabled={!slots.length || busy} onclick={submit}>{$t('customTimes.create')}</Button></Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>
