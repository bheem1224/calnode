<script lang="ts">
import { onMount } from 'svelte';
import { api } from '$lib/api';
import { t } from '$lib/i18n';
import { Button } from '$lib/components/ui/button';
import { Label } from '$lib/components/ui/label';
import * as Select from '$lib/components/ui/select';
import { toast } from 'svelte-sonner';
type Defaults = {
    conferencing_type: string;
    available_conferencing: string[];
};
let data = $state<Defaults | null>(null), error = $state(''), busy = $state(false);
const labels: Record<string, string> = { google_meet: 'Google Meet', teams: 'Microsoft Teams', zoom: 'Zoom', livekit: 'Calnode Video' };
onMount(() => { api.get<Defaults>('/v1/users/me/meeting-defaults').then(d => data = d).catch(e => error = e.message); });
async function save() { busy = true; error = ''; try {
    data = await api.put<Defaults>('/v1/users/me/meeting-defaults', { conferencing_type: data!.conferencing_type });
    toast.success($t('meetingDefaults.saved'));
}
catch (e) {
    error = e instanceof Error ? e.message : $t('dynamicAdvanced.loadError');
}
finally {
    busy = false;
} }
</script>
<section class="space-y-4 rounded-xl border bg-card p-5" aria-labelledby="meeting-defaults-title">
 <div><h3 id="meeting-defaults-title" class="text-sm font-semibold">{$t('meetingDefaults.title')}</h3><p class="mt-1 max-w-xl text-xs leading-relaxed text-muted-foreground">{$t('meetingDefaults.hint')}</p></div>
 {#if error}<p role="alert" class="text-sm text-destructive">{error}</p>{/if}
 {#if data}<div class="flex flex-wrap items-end gap-3"><div class="space-y-2"><Label for="default-conferencing">{$t('meetingDefaults.conferencing')}</Label><Select.Root type="single" bind:value={data.conferencing_type}><Select.Trigger id="default-conferencing" class="w-64 max-w-full justify-between">{labels[data.conferencing_type]||$t('meetingDefaults.automatic')}</Select.Trigger><Select.Content class="w-(--bits-select-anchor-width) min-w-[var(--bits-select-anchor-width)]"><Select.Item value="">{$t('meetingDefaults.automatic')}</Select.Item>{#each data.available_conferencing as kind}<Select.Item value={kind}>{labels[kind]||kind}</Select.Item>{/each}</Select.Content></Select.Root></div><Button type="button" variant="outline" disabled={busy} onclick={save}>{$t('common.save')}</Button></div>{/if}
</section>
