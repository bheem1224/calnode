<script lang="ts">
	import { onMount } from 'svelte';
 import CredentialOwnerPicker from '$lib/components/CredentialOwnerPicker.svelte';
 let credentialOwner=$state('');
 import PermissionEditor from '$lib/components/PermissionEditor.svelte';
 import type { PermissionGrant } from '$lib/api';
 import { currentUser } from '$lib/stores';
 import { Badge } from '$lib/components/ui/badge';
 import * as Select from '$lib/components/ui/select';
	import { api, type APIKey } from '$lib/api';
	import { Button, buttonVariants } from '$lib/components/ui/button';
	import { ConfirmDialog } from '$lib/components/ui/confirm-dialog';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import * as Tooltip from '$lib/components/ui/tooltip';
	import { t, tr, localeTag } from '$lib/i18n';

	let items: APIKey[] = $state([]);
	let loading = $state(true);
	let error = $state('');
	let showCreate = $state(false);
	let newName = $state('');
 let grants=$state<PermissionGrant>({}),expiry=$state('90'),editing=$state('');
 let grantable=$state<PermissionGrant>({});
 const canCreate=$derived(!credentialOwner && !!$currentUser?.permissions?.['api_keys.create']);
	let creating = $state(false);
	let createError = $state('');
	let newKey = $state('');
	let revokeOpen = $state(false);
	let revokeTarget = $state<{ id: string; name: string } | null>(null);

	async function load() {
		try {
			const res = await api.get<{ items: APIKey[] }>(`/v1/api-keys${credentialOwner ? '?user_id='+encodeURIComponent(credentialOwner) : ''}`);
			items = res.items;
 grantable=(await api.get<{delegable:PermissionGrant}>('/v1/permissions')).delegable;
		} catch (e: any) {
			error = e.message;
		} finally {
			loading = false;
		}
	}

	onMount(load);

	async function create() {
		createError = '';
		if (!newName.trim()) { createError = tr('apiKeys.validation.name'); return; }
		creating = true;
		try {
			const payload={name:newName.trim(),permissions:grants,expires_at:new Date(Date.now()+Number(expiry)*86400000).toISOString()};
 if(editing)await api.patch(`/v1/api-keys/${editing}`,payload);else{const res=await api.post<{key:string}>('/v1/api-keys',payload);newKey=res.key;}
 editing='';
			newName = '';
			showCreate = false;
			await load();
		} catch (e: any) {
			createError = e.message;
		} finally {
			creating = false;
		}
	}

	function revoke(id: string, name: string) {
		revokeTarget = { id, name };
		revokeOpen = true;
	}

	async function doRevoke() {
		if (!revokeTarget) return;
		try {
			await api.del(`/v1/api-keys/${revokeTarget.id}`);
			await load();
		} catch (e: any) {
			error = e.message;
		}
	}

	function fmtDate(iso: string) {
		return new Date(iso).toLocaleDateString(localeTag(), { dateStyle: 'medium' });
	}

	function copyKey() {
		navigator.clipboard.writeText(newKey).catch(() => {});
	}
</script>

<ConfirmDialog
	bind:open={revokeOpen}
	title={$t('apiKeys.confirm.title')}
	description={revokeTarget ? $t('apiKeys.confirm.description', { name: revokeTarget.name }) : ''}
	confirmText={$t('apiKeys.confirm.confirm')}
	destructive
	onConfirm={doRevoke}
/>

<svelte:head><title>{$t('apiKeys.headTitle')}</title></svelte:head>

<div class="mb-8 flex items-center justify-between">
	<div>
		<h1 class="text-2xl font-semibold tracking-tight">{$t('apiKeys.title')}</h1>
		<p class="mt-1 text-sm text-muted-foreground">{$t('apiKeys.subtitle')}</p>
	</div>
	{#if canCreate}<Button onclick={() => { showCreate = !showCreate; editing='';grants={};createError = ''; newKey = ''; }}>
		{showCreate ? $t('apiKeys.new.toggleCancel') : $t('apiKeys.new.toggleOpen')}
	</Button>{/if}
</div>

<CredentialOwnerPicker bind:value={credentialOwner} permission="api_keys.manage" onChange={load} />

{#if newKey}
	<div class="mb-6 rounded-lg border border-emerald-500/25 bg-emerald-500/10 p-4">
		<p class="mb-2 text-sm font-medium text-emerald-800 dark:text-emerald-300">{$t('apiKeys.created.banner')}</p>
		<div class="mb-3 rounded-md border bg-card px-3 py-2 font-mono text-xs text-foreground break-all">{newKey}</div>
		<Button variant="outline" size="sm" onclick={copyKey}>
			{$t('apiKeys.copyToClipboard')}
		</Button>
	</div>
{/if}

{#if showCreate && !credentialOwner}
	<div class="mb-6 w-full max-w-3xl rounded-lg border bg-card p-6">
		<h2 class="mb-4 text-sm font-semibold">{editing ? $t('permissions.review') : $t('apiKeys.new.title')}</h2>
		{#if createError}<p class="mb-3 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{createError}</p>{/if}
		<div class="mb-4 max-w-sm space-y-1.5">
			<Label for="key-name">{$t('apiKeys.new.nameLabel')}</Label>
			<Input
				id="key-name"
 disabled={!!editing}
				bind:value={newName}
				placeholder={$t('apiKeys.new.namePlaceholder')}
			/>
		</div>
  <div class="mb-4 space-y-1.5"><Label id="key-expiry-label">{$t('permissions.expiry')}</Label><Select.Root type="single" bind:value={expiry}><Select.Trigger aria-labelledby="key-expiry-label" class="w-40 rounded-lg">{expiry} {$t('permissions.days')}</Select.Trigger><Select.Content>{#each ['7','30','90','365'] as days}<Select.Item value={days}>{days} {$t('permissions.days')}</Select.Item>{/each}</Select.Content></Select.Root></div>
  <div class="mb-5"><PermissionEditor bind:value={grants} delegated /></div>
		<Button onclick={create} disabled={creating || !Object.keys(grants).length}>
			{creating ? $t('apiKeys.new.creating') : editing ? $t('permissions.save') : $t('apiKeys.new.create')}
		</Button>
	</div>
{/if}

{#if error}<p class="mb-4 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</p>{/if}

{#if loading}
	<p class="py-8 text-sm text-muted-foreground">{$t('common.loading')}</p>
{:else if items.length === 0}
	<div class="rounded-lg border border-dashed bg-card p-12 text-center">
		<p class="text-sm font-medium">{$t('apiKeys.empty.title')}</p>
		<p class="mt-1 text-sm text-muted-foreground">{$t('apiKeys.empty.description')}</p>
	</div>
{:else}
	<div class="overflow-x-auto rounded-lg border bg-card">
		<table class="w-full text-sm">
			<thead>
				<tr class="border-b">
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">{$t('apiKeys.table.name')}</th>
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">{$t('apiKeys.table.created')}</th>
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">{$t('apiKeys.table.lastUsed')}</th>
					<th class="px-4 pb-3 pt-3"></th>
				</tr>
			</thead>
			<tbody class="divide-y">
				<Tooltip.Provider>
					{#each items as k}
						<tr class="transition-colors hover:bg-muted/30">
							<td class="px-4 py-3 font-medium"><div class="flex flex-wrap items-center gap-2">{k.name}{#if k.legacy}<Badge variant="outline">{$t('permissions.legacy')}</Badge>{/if}</div><p class="mt-1 text-xs font-normal text-muted-foreground">{Object.keys(k.permissions||{}).length} {$t('permissions.selected')}{#if k.expires_at} · {$t('permissions.expires')} {fmtDate(k.expires_at)}{/if}</p></td>
							<td class="px-4 py-3 text-muted-foreground">{fmtDate(k.created_at)}</td>
							<td class="px-4 py-3 text-muted-foreground">
								{#if k.last_used_at}{fmtDate(k.last_used_at)}{:else}{$t('common.never')}{/if}
							</td>
							<td class="px-4 py-3 text-right">
 {#if canCreate}<Button type="button" variant="outline" size="sm" onclick={()=>{editing=k.id;newName=k.name;expiry='90';grants=Object.fromEntries(Object.entries(k.permissions||{}).filter(([id])=>grantable[id]).map(([id,scope])=>[id,({own:1,team:2,all:3}[scope] > {own:1,team:2,all:3}[grantable[id]]) ? grantable[id] : scope]));showCreate=true;}}>{$t('permissions.review')}</Button>{/if}
								<Tooltip.Root>
									<Tooltip.Trigger class={buttonVariants({ variant: 'ghost', size: 'icon' })} onclick={() => revoke(k.id, k.name)}>
										<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/><path d="M10 11v6"/><path d="M14 11v6"/><path d="M9 6V4h6v2"/></svg>
									</Tooltip.Trigger>
									<Tooltip.Content>{$t('apiKeys.tooltip.revoke')}</Tooltip.Content>
								</Tooltip.Root>
							</td>
						</tr>
					{/each}
				</Tooltip.Provider>
			</tbody>
		</table>
	</div>
{/if}
