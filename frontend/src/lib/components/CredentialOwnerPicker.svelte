<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type TeamMember } from '$lib/api';
	import { currentUser } from '$lib/stores';
	import { Combobox } from '$lib/components/ui/combobox';
	import { Label } from '$lib/components/ui/label';
	import { t } from '$lib/i18n';

	let { value = $bindable(''), permission, onChange }: {
		value: string;
		permission: string;
		onChange: () => void;
	} = $props();
	let members = $state<TeamMember[]>([]);
	const canBrowse = $derived(
		!!$currentUser?.permissions?.['members.read'] &&
		['team', 'all'].includes($currentUser?.permissions?.[permission] || '')
	);
	const items = $derived([
		{ value: '', label: $t('permissions.myAccount') },
		...members.filter(m => m.id !== $currentUser?.id && !m.archived)
			.map(m => ({ value: m.id, label: `${m.name} (${m.email})` }))
	]);
	onMount(async () => {
		if (!canBrowse) return;
		try { members = await api.get<TeamMember[]>('/v1/users'); } catch { /* Own inventory remains available. */ }
	});
</script>

{#if canBrowse}
	<div class="mb-5 max-w-sm space-y-1.5">
		<Label for={`credential-owner-${permission}`}>{$t('permissions.account')}</Label>
		<Combobox id={`credential-owner-${permission}`} {items} bind:value onValueChange={onChange} />
	</div>
{/if}
