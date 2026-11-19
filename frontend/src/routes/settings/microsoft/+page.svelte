<script lang="ts">
 import { onMount } from 'svelte';
 import { goto } from '$app/navigation';
 import { base } from '$lib/paths';
 import { t } from '$lib/i18n';
 onMount(()=>{goto(`${base}/settings/identity`,{replaceState:true});});
</script>
<p class="text-sm text-muted-foreground">{$t('securitySettings.loading')}</p>
