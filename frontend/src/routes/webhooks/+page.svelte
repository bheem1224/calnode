<script lang="ts">
	import WebhooksManager from '$lib/components/WebhooksManager.svelte';
	import { t } from '$lib/i18n';
</script>

<svelte:head><title>{$t('webhooks.headTitle')}</title></svelte:head>

<WebhooksManager />
