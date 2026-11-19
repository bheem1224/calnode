<script lang="ts">
import { untrack } from 'svelte';
import { api } from '$lib/api';
import { currentUser } from '$lib/stores';
import * as Dialog from '$lib/components/ui/dialog';
import { Button } from '$lib/components/ui/button';
import { Input } from '$lib/components/ui/input';
import { Label } from '$lib/components/ui/label';
import * as Select from '$lib/components/ui/select';
import { Checkbox } from '$lib/components/ui/checkbox';
import { t, tr } from '$lib/i18n';
import { toast } from 'svelte-sonner';
let { open = $bindable(false) }: {
    open: boolean;
} = $props();
let users = $state<{
    id: string;
    name: string;
    handle: string;
}[]>([]), query = $state(''), selected = $state<string[]>([]), error = $state('');
const filtered = $derived(users.filter(u => `${u.name} ${u.handle}`.toLowerCase().includes(query.trim().toLowerCase())));
type Options = {
    location_type: string;
    calendars: {
        id: string;
        label: string;
        provider: string;
    }[];
};
let advanced = $state(false), organizer = $state(''), meetingName = $state(''), duration = $state(30), durations = $state('30'), location = $state(''), locationValue = $state(''), destination = $state(''), options = $state<Options | null>(null), loading = $state(false);
const locationLabels = $derived<Record<string, string>>({ google_meet: 'Google Meet', teams: 'Microsoft Teams', zoom: 'Zoom', livekit: 'Calnode Video', in_person: $t('dynamicAdvanced.inPerson'), link: $t('dynamicAdvanced.video'), phone: $t('dynamicAdvanced.phone'), attendee_phone: $t('dynamicAdvanced.attendeePhone') });
let optionsRequest = 0;
async function loadOptions(hosts: string[], host: string) { const request = ++optionsRequest; loading = true; options = null; error = ''; path = ''; locationValue = ''; destination = ''; try {
    const result = await api.get<Options>('/v1/dynamic-links/options?host_ids=' + encodeURIComponent(hosts.join(',')) + '&organizer_id=' + encodeURIComponent(host));
    if (request === optionsRequest) {
        options = result;
        location = result.location_type;
    }
}
catch {
    if (request === optionsRequest)
        error = tr('dynamicAdvanced.loadError');
}
finally {
    if (request === optionsRequest)
        loading = false;
} }
function choose(id: string, checked: boolean) { path = ''; selected = checked ? [...selected, id] : selected.filter(x => x !== id); if (!selected.includes(organizer))
    organizer = selected[0] || ''; if (selected.length >= 2)
    void loadOptions([...selected], organizer);
else {
    optionsRequest++;
    options = null;
    loading = false;
} }
let path = $state(''), busy = $state(false);
$effect(() => { meetingName; duration; durations; location; locationValue; destination; path = ''; });
const canMint = $derived(selected.length >= 2 && !loading && !!options);
async function mint() { if (path)
    return path; busy = true; try {
    const result = await api.post<{
        url: string;
    }>('/v1/dynamic-links', { host_ids: selected, organizer_id: organizer, name: meetingName, duration_minutes: Number(duration), duration_options: [...new Set([...durations.split(',').filter(v => v.trim()).map(v => Number(v.trim())), Number(duration)])], location_type: location, location_value: locationValue, destination_id: destination });
    path = result.url;
    return path;
}
finally {
    busy = false;
} }
$effect(() => { if (open)
    untrack(() => { query = ''; selected = []; organizer = ''; advanced = false; meetingName = ''; duration = 30; durations = '30'; options = null; optionsRequest++; loading = false; path = ''; error = ''; api.get<{
        items: {
            id: string;
            name: string;
            handle: string;
        }[];
    }>('/v1/users/directory').then(result => { const me = $currentUser; users = [...(me ? [{ id: me.id, name: me.name, handle: me.handle || '' }] : []), ...result.items.filter(u => u.id !== me?.id)]; }).catch(() => { users = []; error = tr('dynamicLink.loadError'); }); }); });
async function copy() { try {
    await navigator.clipboard.writeText(await mint());
    toast.success(tr('dynamicLink.copied'));
}
catch (e) {
    error = e instanceof Error ? e.message : tr('dynamicLink.copyError');
    toast.error(error);
} }
</script>
<Dialog.Root bind:open>
  <Dialog.Content class="max-h-[90dvh] overflow-y-auto sm:max-w-lg">
    <Dialog.Header><Dialog.Title>{$t('dynamicLink.title')}</Dialog.Title><Dialog.Description>{$t('dynamicLink.description')}</Dialog.Description></Dialog.Header>
    <Input bind:value={query} placeholder={$t('dynamicLink.search')} aria-label={$t('dynamicLink.search')} />
    <div class="max-h-56 divide-y overflow-y-auto rounded-lg border">
      {#if error}<p class="p-4 text-sm text-destructive">{error}</p>{:else if !filtered.length}<p class="p-4 text-sm text-muted-foreground">{$t('dynamicLink.empty')}</p>{/if}
      {#each filtered as u (u.id)}
        {@const handle = u.id}
        <label class="flex cursor-pointer items-center gap-3 p-3 hover:bg-accent/50"><Checkbox disabled={busy} checked={selected.includes(handle)} onCheckedChange={(checked) => choose(handle,!!checked)} /><span class="min-w-0"><span class="block truncate text-sm font-medium">{u.name}</span><span class="block truncate text-xs text-muted-foreground">{u.handle ? `@${u.handle}` : $t('dynamicLink.noHandle')}</span></span></label>
      {/each}
    </div>
    {#if selected.length>=2}
      <div class="space-y-2"><Label for="dynamic-organizer">{$t('dynamicAdvanced.organizer')}</Label><Select.Root disabled={busy} type="single" value={organizer} onValueChange={v=>{if(v){organizer=v;void loadOptions([...selected],v);}}}><Select.Trigger id="dynamic-organizer" class="w-full justify-between">{users.find(u=>u.id===organizer)?.name}</Select.Trigger><Select.Content class="w-(--bits-select-anchor-width) min-w-[var(--bits-select-anchor-width)]">{#each selected as id}<Select.Item value={id}>{users.find(u=>u.id===id)?.name}</Select.Item>{/each}</Select.Content></Select.Root></div>
      <Button type="button" variant="ghost" class="justify-start px-0" aria-expanded={advanced} onclick={()=>advanced=!advanced}>{$t('dynamicAdvanced.title')} {advanced?'−':'+'}</Button>
      {#if loading}<p role="status" class="text-xs text-muted-foreground">{$t('dynamicAdvanced.loading')}</p>{/if}
      {#if advanced}<div class="space-y-4 rounded-lg border bg-muted/20 p-4">
        <p class="text-xs leading-relaxed text-muted-foreground">{$t('dynamicAdvanced.hint')}</p>
        <div class="space-y-2"><Label for="dynamic-title">{$t('dynamicAdvanced.name')}</Label><Input disabled={busy} id="dynamic-title" maxlength={120} bind:value={meetingName} /></div>
        <div class="grid items-end gap-4 sm:grid-cols-2"><div class="space-y-2"><Label for="dynamic-duration">{$t('dynamicAdvanced.duration')}</Label><Input disabled={busy} id="dynamic-duration" type="number" min={15} max={180} bind:value={duration} /></div><div class="space-y-2"><Label for="dynamic-durations">{$t('dynamicAdvanced.durations')}</Label><Input disabled={busy} id="dynamic-durations" bind:value={durations} /></div></div>
        <div class="space-y-2"><Label for="dynamic-location">{$t('dynamicAdvanced.location')}</Label><Select.Root disabled={busy} type="single" bind:value={location}><Select.Trigger id="dynamic-location" class="w-full justify-between">{locationLabels[location]||location}</Select.Trigger><Select.Content class="w-(--bits-select-anchor-width) min-w-[var(--bits-select-anchor-width)]">{#each Object.entries(locationLabels) as [value,label]}<Select.Item {value}>{label}</Select.Item>{/each}</Select.Content></Select.Root></div>
        {#if location!=='livekit'&&location!=='attendee_phone'}<div class="space-y-2"><Label for="dynamic-location-value">{$t('dynamicAdvanced.value')}</Label><Input disabled={busy} id="dynamic-location-value" maxlength={2048} bind:value={locationValue} /></div>{/if}
        <div class="space-y-2"><Label for="dynamic-calendar">{$t('dynamicAdvanced.calendar')}</Label><Select.Root disabled={busy} type="single" bind:value={destination}><Select.Trigger id="dynamic-calendar" class="w-full justify-between">{options?.calendars.find(c=>c.id===destination)?.label||$t('dynamicAdvanced.defaultCalendar')}</Select.Trigger><Select.Content class="w-(--bits-select-anchor-width) min-w-[var(--bits-select-anchor-width)]"><Select.Item value="">{$t('dynamicAdvanced.defaultCalendar')}</Select.Item>{#each options?.calendars||[] as cal}<Select.Item value={cal.id}>{cal.label}</Select.Item>{/each}</Select.Content></Select.Root></div>
      </div>{/if}
    {/if}
    {#if error}<p role="alert" class="text-xs text-destructive">{error}</p>{/if}
    {#if path}<p class="truncate rounded bg-muted px-2.5 py-1.5 font-mono text-xs">{path}</p>{:else if selected.length<2}<p class="text-xs text-muted-foreground">{$t('dynamicLink.minimum')}</p>{/if}
    <Dialog.Footer><Button type="button" variant="ghost" onclick={() => open = false}>{$t('common.close')}</Button><Button type="button" variant="outline" disabled={!canMint||busy} onclick={copy}>{$t('dynamicLink.copy')}</Button><Button type="button" disabled={!canMint||busy} onclick={async () => { try{const url=await mint();window.location.assign(url);}catch(e){error=e instanceof Error?e.message:tr('dynamicLink.copyError');} }}>{$t('dynamicLink.open')}</Button></Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>
