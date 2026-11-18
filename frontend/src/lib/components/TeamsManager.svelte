<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type Team, type TeamMember } from '$lib/api';
	import { currentUser } from '$lib/stores';
	import { Button } from '$lib/components/ui/button';
	import { ConfirmDialog } from '$lib/components/ui/confirm-dialog';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { Badge } from '$lib/components/ui/badge';
	import * as Select from '$lib/components/ui/select';
	import { toast } from 'svelte-sonner';
	import { t, tr } from '$lib/i18n';

	let teams = $state<Team[]>([]);
	type TeamCandidate=Pick<TeamMember,'id'|'name'> & Partial<Pick<TeamMember,'email'|'archived'>>;
 let users = $state<TeamCandidate[]>([]);
	let loading = $state(true);
	let error = $state('');

	// Create form
	let showCreate = $state(false);
	let newTeamName = $state('');
	let creating = $state(false);

	// Expanded team detail (teamId → full team with members)
	let expanded = $state<Record<string, Team>>({});
	// Add-member selection per team (teamId → userId)
	let addChoice = $state<Record<string, string>>({});
	// Rename state
	let renamingId = $state<string | null>(null);
	let renameValue = $state('');
	// Team profile edits (teamId -> fields)
	let profileEdits = $state<Record<string, { description: string; logo_url: string; location: string; brand_color: string; show_members_publicly: boolean }>>({});
	let savingProfile = $state<string | null>(null);

	function initProfileEdit(detail: Team) {
		profileEdits = {
			...profileEdits,
			[detail.id]: {
				description: detail.description ?? '',
				logo_url: detail.logo_url ?? '',
				location: detail.location ?? '',
				brand_color: detail.brand_color ?? '',
				show_members_publicly: !!detail.show_members_publicly,
			},
		};
	}

	async function saveTeamProfile(teamId: string) {
		const p = profileEdits[teamId];
		if (!p) return;
		savingProfile = teamId;
		try {
			const updated = await api.patch<Team>(`/v1/teams/${teamId}`, p);
			expanded = { ...expanded, [teamId]: updated };
			teams = teams.map((t) => (t.id === teamId ? { ...t, ...updated } : t));
			toast.success('Team profile saved');
		} catch (e: any) {
			toast.error(e.message || tr('common.failed'));
		} finally {
			savingProfile = null;
		}
	}

	function canManage(team: Team, action = 'teams.update'): boolean {
        if(action==='teams.members')return Boolean(($currentUser?.is_admin||$currentUser?.is_owner)&&$currentUser?.permissions?.['teams.members']);
        const scope=$currentUser?.permissions?.[action];
        if($currentUser?.permissions) return scope==='all' || (scope==='team' && !!team.my_role) || (scope==='own' && team.my_role==='owner');
        return Boolean($currentUser?.is_admin || team.my_role==='owner');
    }

	function copyTeamUrl(slug: string) {
		const url = `${window.location.origin}/team/${slug}`;
		navigator.clipboard?.writeText(url);
		toast.success(`Copied ${url}`);
	}

	// Confirm dialog
	let confirmOpen = $state(false);
	let confirmTitle = $state('');
	let confirmDescription = $state('');
	let pendingAction: (() => void) | null = null;
	function openConfirm(o: { title: string; description: string; action: () => void }) {
		confirmTitle = o.title; confirmDescription = o.description; pendingAction = o.action; confirmOpen = true;
	}

	async function load() {
		try {
			const [teamsRes, usersRes] = await Promise.all([
				api.get<{ items: Team[] }>('/v1/teams'),
				$currentUser?.permissions?.['members.read'] ? api.get<TeamMember[]>('/v1/users') : api.get<{items:TeamCandidate[]}>('/v1/users/directory').then(r=>r.items)
			]);
			teams = teamsRes.items;
			users = usersRes;
		} catch (e: any) {
			error = e.message;
		} finally {
			loading = false;
		}
	}
	onMount(load);

	async function createTeam() {
		if (!newTeamName.trim()) { toast.error(tr('teams.toast.nameRequired')); return; }
		creating = true;
		try {
			await api.post('/v1/teams', { name: newTeamName.trim() });
			newTeamName = '';
			showCreate = false;
			await load();
		} catch (e: any) {
			toast.error(e.message || tr('common.failed'));
		} finally {
			creating = false;
		}
	}

	async function toggleExpand(team: Team) {
		if (expanded[team.id]) {
			const { [team.id]: _, ...rest } = expanded;
			expanded = rest;
			return;
		}
		try {
			const detail = await api.get<Team>(`/v1/teams/${team.id}`);
			expanded = { ...expanded, [team.id]: detail };
			initProfileEdit(detail);
		} catch (e: any) {
			toast.error(e.message || tr('common.failed'));
		}
	}

	async function refreshTeam(teamId: string) {
		const detail = await api.get<Team>(`/v1/teams/${teamId}`);
		expanded = { ...expanded, [teamId]: detail };
		// Keep the member counts in the list fresh too.
		teams = teams.map((t) => (t.id === teamId ? { ...t, member_count: detail.member_count } : t));
	}

	function membersNotIn(team: Team): TeamCandidate[] {
		const inTeam = new Set((expanded[team.id]?.members ?? []).map((m) => m.id));
		return users.filter((u) => !u.archived && !inTeam.has(u.id));
	}

	async function addMember(teamId: string) {
		const userId = addChoice[teamId];
		if (!userId) return;
		try {
			await api.post(`/v1/teams/${teamId}/members`, { user_id: userId });
			addChoice = { ...addChoice, [teamId]: '' };
			await refreshTeam(teamId);
		} catch (e: any) {
			toast.error(e.message || tr('common.failed'));
		}
	}

	async function removeMember(teamId: string, userId: string) {
		try {
			await api.del(`/v1/teams/${teamId}/members/${userId}`);
			await refreshTeam(teamId);
		} catch (e: any) {
			toast.error(e.message || tr('common.failed'));
		}
	}

	async function savePriority(teamId: string, userId: string, value: number) {
		try {
			await api.patch(`/v1/teams/${teamId}/members/${userId}`, { routing_priority: Number(value) });
		} catch (e: any) {
			toast.error(e.message || tr('common.failed'));
			await refreshTeam(teamId);
		}
	}

	async function saveRole(teamId: string, userId: string, role: 'owner' | 'member') {
		try {
			await api.patch(`/v1/teams/${teamId}/members/${userId}`, { role });
			await refreshTeam(teamId);
			toast.success('Role updated');
		} catch (e: any) {
			toast.error(e.message || tr('common.failed'));
			await refreshTeam(teamId);
		}
	}

	function startRename(team: Team) { renamingId = team.id; renameValue = team.name; }
	function cancelRename() { renamingId = null; renameValue = ''; }
	async function saveRename(team: Team) {
		if (!renameValue.trim()) { toast.error(tr('teams.toast.nameEmpty')); return; }
		try {
			await api.patch(`/v1/teams/${team.id}`, { name: renameValue.trim() });
			renamingId = null;
			await load();
		} catch (e: any) {
			toast.error(e.message || tr('common.failed'));
		}
	}

	function deleteTeam(team: Team) {
		openConfirm({
			title: tr('teams.confirm.delete.title', { name: team.name }),
			description: tr('teams.confirm.delete.description'),
			action: async () => {
				try {
					await api.del(`/v1/teams/${team.id}`);
					const { [team.id]: _, ...rest } = expanded;
					expanded = rest;
					await load();
				} catch (e: any) {
					toast.error(e.message || tr('common.failed'));
				}
			}
		});
	}
</script>

<ConfirmDialog bind:open={confirmOpen} title={confirmTitle} description={confirmDescription} confirmText={$t('teams.confirm.delete.confirm')} destructive onConfirm={() => pendingAction?.()} />

<svelte:head><title>{$t('teams.headTitle')}</title></svelte:head>

<div class="mb-8 flex items-center justify-between">
	<div>
		<h1 class="text-2xl font-semibold tracking-tight">{$t('teams.title')}</h1>
		<p class="mt-1 text-sm text-muted-foreground">{$t('teams.subtitle')}</p>
	</div>
	{#if $currentUser?.permissions?.['teams.create'] || (!$currentUser?.permissions && $currentUser?.is_admin)}
		<Button onclick={() => { showCreate = !showCreate; }}>{showCreate ? $t('common.cancel') : $t('teams.newTeam')}</Button>
	{/if}
</div>

{#if showCreate}
	<div class="mb-6 rounded-lg border bg-card p-6">
		<h2 class="mb-4 text-sm font-semibold">{$t('teams.newTeamForm.title')}</h2>
		<div class="flex items-end gap-3">
			<div class="flex-1 space-y-1.5">
				<Label for="team-name">{$t('teams.nameLabel')}</Label>
				<Input id="team-name" bind:value={newTeamName} placeholder={$t('teams.namePlaceholder')} onkeydown={(e) => e.key === 'Enter' && createTeam()} />
			</div>
			<Button onclick={createTeam} disabled={creating}>{creating ? $t('teams.creating') : $t('teams.create')}</Button>
		</div>
	</div>
{/if}

{#if error}<p class="mb-4 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</p>{/if}

{#if loading}
	<p class="py-8 text-sm text-muted-foreground">{$t('common.loading')}</p>
{:else if teams.length === 0}
	<div class="rounded-lg border border-dashed bg-card p-12 text-center">
		<p class="text-sm font-medium">{$t('teams.empty.title')}</p>
		<p class="mt-1 text-sm text-muted-foreground">{$t('teams.empty.description')}</p>
	</div>
{:else}
	<div class="space-y-3">
		{#each teams as team (team.id)}
			<div class="rounded-xl border bg-card transition-colors hover:border-foreground/20">
				<div
					class="flex flex-col gap-3 p-4 sm:flex-row sm:items-center sm:justify-between"
				>
					<div class="min-w-0">
						{#if renamingId === team.id}
							<div class="flex flex-wrap items-center gap-2">
								<Input bind:value={renameValue} class="h-8 w-56" onkeydown={(e) => e.key === 'Enter' && saveRename(team)} />
								<Button size="sm" class="h-8" onclick={() => saveRename(team)}>{$t('common.save')}</Button>
								<Button size="sm" variant="ghost" class="h-8" onclick={cancelRename}>{$t('common.cancel')}</Button>
							</div>
						{:else}
							<div class="flex flex-wrap items-center gap-2">
								<p class="font-medium">{team.name}</p>
								<Badge variant="outline" class="font-mono text-xs">
									<a href={`/team/${team.slug}`} target="_blank" rel="noopener noreferrer" class="hover:underline" onclick={(e) => e.stopPropagation()}>
										/team/{team.slug}
									</a>
								</Badge>
								{#if team.my_role === 'owner'}
									<Badge variant="secondary" class="text-[11px]">Team Head</Badge>
								{/if}
							</div>
							<p class="mt-0.5 text-xs text-muted-foreground">{$t('teams.memberCount', { count: team.member_count })}</p>
						{/if}
					</div>
					<div class="flex shrink-0 flex-wrap items-center gap-1">
						<Button size="sm" variant="ghost" class="h-8 text-xs" onclick={() => copyTeamUrl(team.slug)}>Copy link</Button>
						<Button size="sm" variant="outline" class="h-8 text-xs" onclick={() => toggleExpand(team)}>
							{expanded[team.id] ? $t('teams.close') : $t('teams.manage')}
						</Button>
						{#if canManage(team)}
							<Button size="sm" variant="ghost" class="h-8 text-xs" onclick={() => startRename(team)}>{$t('teams.rename')}</Button>
						{/if}
						{#if canManage(team,'teams.delete')}
							<Button size="sm" variant="ghost" class="h-8 text-xs text-destructive hover:text-destructive" onclick={() => deleteTeam(team)}>{$t('common.delete')}</Button>
						{/if}
					</div>
				</div>

				{#if expanded[team.id]}
					<div class="border-t px-4 py-4">
						<h3 class="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">{$t('teams.members.title')}</h3>
						{#if (expanded[team.id].members ?? []).length === 0}
							<p class="mb-3 text-sm text-muted-foreground">{$t('teams.members.empty')}</p>
						{:else}
							<div class="mb-3 overflow-x-auto">
							<table class="w-full min-w-[460px] text-sm">
								<thead>
									<tr class="border-b">
										<th class="pb-2 text-left text-xs font-medium text-muted-foreground">{$t('teams.members.column')}</th>
										<th class="pb-2 text-left text-xs font-medium text-muted-foreground">Role</th>
										<th class="pb-2 text-left text-xs font-medium text-muted-foreground">{$t('teams.members.routingPriority')}</th>
										<th class="pb-2"></th>
									</tr>
								</thead>
								<tbody class="divide-y">
									{#each expanded[team.id].members ?? [] as m (m.id)}
										<tr>
											<td class="py-2">
												<div class="flex items-center gap-2">
													<span class="font-medium">{m.name}</span>
													<span class="text-xs text-muted-foreground">{m.email}</span>
													{#if m.archived}<Badge variant="outline" class="text-xs text-muted-foreground">{$t('members.badge.archived')}</Badge>{/if}
												</div>
											</td>
											<td class="py-2">
												{#if canManage(team,'teams.members')}
													<Select.Root
														type="single"
														value={m.role ?? 'member'}
														onValueChange={(v) => {
															if (v === 'owner' || v === 'member') {
																saveRole(team.id, m.id, v);
															}
														}}
													>
														<Select.Trigger class="h-7 w-32 text-xs">
															{(m.role ?? 'member') === 'owner' ? 'Team Head' : 'Member'}
														</Select.Trigger>
														<Select.Content>
															<Select.Item value="member" label="Member">Member</Select.Item>
															<Select.Item value="owner" label="Team Head">Team Head</Select.Item>
														</Select.Content>
													</Select.Root>
												{:else}
													<span class="text-xs text-muted-foreground">{m.role === 'owner' ? 'Team Head' : 'Member'}</span>
												{/if}
											</td>
											<td class="py-2">
												<Input
													type="number"
													value={m.routing_priority}
													disabled={!canManage(team,'teams.members')}
													class="h-7 w-20 text-xs"
													onchange={(e) => savePriority(team.id, m.id, +(e.currentTarget as HTMLInputElement).value)}
												/>
											</td>
											<td class="py-2 text-right">
												{#if canManage(team,'teams.members')}
													<Button size="sm" variant="ghost" class="h-7 text-xs text-destructive hover:text-destructive" onclick={() => removeMember(team.id, m.id)}>{$t('teams.members.remove')}</Button>
												{/if}
											</td>
										</tr>
									{/each}
								</tbody>
							</table>
							</div>
						{/if}

						{#if canManage(team,'teams.members') && users.length > 0}
							<!-- Add a member -->
							<div class="flex items-center gap-2">
								<Select.Root
									type="single"
									value={addChoice[team.id] ?? ''}
									onValueChange={(v) => { addChoice = { ...addChoice, [team.id]: v ?? '' }; }}
									disabled={membersNotIn(team).length === 0}
								>
									<Select.Trigger class="w-fit min-w-48">
										{#if addChoice[team.id]}
											{@const u = users.find((x) => x.id === addChoice[team.id])}
											{u ? u.email ? `${u.name} (${u.email})` : u.name : $t('teams.addMember')}
										{:else}
											{$t('teams.addMember')}
										{/if}
									</Select.Trigger>
									<Select.Content>
										{#each membersNotIn(team) as u}
											<Select.Item value={u.id} label={u.email ? `${u.name} (${u.email})` : u.name}>{u.name}{u.email ? ` (${u.email})` : ''}</Select.Item>
										{/each}
									</Select.Content>
								</Select.Root>
								<Button size="sm" variant="outline" class="h-8" disabled={!addChoice[team.id]} onclick={() => addMember(team.id)}>{$t('teams.add')}</Button>
								{#if membersNotIn(team).length === 0}
									<span class="text-xs text-muted-foreground">{$t('teams.allMembersInTeam')}</span>
								{/if}
							</div>
						{/if}
						<p class="mt-2 text-xs text-muted-foreground">{$t('teams.routingHint')}</p>

						{#if canManage(team) && profileEdits[team.id]}
							<div class="mt-5 border-t pt-4">
								<div class="mb-3 flex items-center justify-between">
									<h3 class="text-xs font-semibold uppercase tracking-wide text-muted-foreground">Team Profile &amp; Branding</h3>
									<Button size="sm" class="h-7 text-xs" disabled={savingProfile === team.id} onclick={() => saveTeamProfile(team.id)}>
										{savingProfile === team.id ? 'Saving...' : 'Save profile'}
									</Button>
								</div>
								<div class="grid gap-3 sm:grid-cols-2">
									<div class="sm:col-span-2 space-y-1">
										<Label class="text-xs">Description / About</Label>
										<textarea
											rows="2"
											bind:value={profileEdits[team.id].description}
											placeholder="A few sentences about your team for your public team booking page..."
											class="flex w-full rounded-md border border-input bg-transparent px-3 py-1.5 text-sm shadow-xs outline-none focus-visible:ring-1 focus-visible:ring-ring dark:bg-input/30"
										></textarea>
									</div>
									<div class="space-y-1">
										<Label class="text-xs">Logo URL</Label>
										<Input bind:value={profileEdits[team.id].logo_url} placeholder="https://..." class="h-8 text-xs" />
									</div>
									<div class="space-y-1">
										<Label class="text-xs">Location</Label>
										<Input bind:value={profileEdits[team.id].location} placeholder="e.g. San Francisco, CA / Remote" class="h-8 text-xs" />
									</div>
									<label class="sm:col-span-2 flex cursor-pointer items-center justify-between gap-3 rounded-md border px-3 py-2">
										<div>
											<span class="text-xs font-medium">Show team members on public page</span>
											<p class="text-[11px] text-muted-foreground">Keep off so visitors book the team directly without picking a specific member.</p>
										</div>
										<input
											type="checkbox"
											bind:checked={profileEdits[team.id].show_members_publicly}
											class="h-4 w-4 rounded border-border accent-primary"
										/>
									</label>
								</div>
							</div>
						{/if}
					</div>
				{/if}
			</div>
		{/each}
	</div>
{/if}
