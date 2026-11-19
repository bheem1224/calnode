<script lang="ts">
import { onMount } from 'svelte';
import { api } from '$lib/api';
import { t } from '$lib/i18n';
let minimum = $state(16);
onMount(() => { api.get<{
    password_min_length: number;
}>('/v1/auth/status').then(s => minimum = s.password_min_length || 16).catch(() => { }); });
</script>
<p class="text-xs leading-relaxed text-muted-foreground">{$t('passwordPolicy.guidance',{count:minimum})}</p>
