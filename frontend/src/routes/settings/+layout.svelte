<script lang="ts">
	import { page } from '$app/stores';
	import { base } from '$app/paths';
	import { currentUser } from '$lib/stores';
	import { t } from '$lib/i18n';
	import type { Snippet } from 'svelte';

	let { children }: { children: Snippet } = $props();

	const navItems = $derived([
		{ section: '', href: `${base}/settings`, label: 'Overview', exact: true },
		{ section: 'Personal Account', href: `${base}/settings/profile`, label: $t('settings.nav.profile') },
		{ section: 'Personal Account', href: `${base}/settings/notifications`, label: $t('settings.nav.notifications') },
		{ section: 'Team Settings', href: `${base}/teams`, label: 'Teams & Routing' },
		{ section: 'Instance Settings', href: `${base}/settings/branding`, label: $t('settings.nav.branding'), adminOnly: true },
		{ section: 'Instance Settings', href: `${base}/settings/email`, label: 'Email & RSVP', adminOnly: true },
		{ section: 'Instance Settings', href: `${base}/settings/google`, label: $t('settings.nav.google'), adminOnly: true },
		{ section: 'Instance Settings', href: `${base}/settings/microsoft`, label: 'Microsoft OAuth', adminOnly: true },
		{ section: 'Instance Settings', href: `${base}/settings/zoom`, label: $t('settings.nav.zoom'), adminOnly: true },
		{ section: 'Instance Settings', href: `${base}/settings/video`, label: $t('settings.nav.video'), adminOnly: true },
		{ section: 'Instance Settings', href: `${base}/settings/payments`, label: $t('settings.nav.payments'), adminOnly: true },
		{ section: 'Instance Settings', href: `${base}/settings/storage`, label: $t('settings.nav.storage'), adminOnly: true },
		{ section: 'Instance Settings', href: `${base}/settings/ai`, label: $t('settings.nav.ai'), adminOnly: true },
		{ section: 'Instance Settings', href: `${base}/settings/tracking`, label: $t('settings.nav.tracking'), adminOnly: true },
		{ section: 'Instance Settings', href: `${base}/team`, label: 'Members & Roles', adminOnly: true },
	]);

	const visibleNavItems = $derived(navItems.filter((item) => !item.adminOnly || $currentUser?.is_admin));

	function isActive(href: string, exact?: boolean): boolean {
		const path = $page.url.pathname.replace(/\/+$/, '');
		const target = href.replace(/\/+$/, '');
		if (exact) return path === target;
		return path.startsWith(target);
	}
</script>

<svelte:head><title>{$t('settings.headTitle')}</title></svelte:head>

<div class="mb-8">
	<h1 class="text-2xl font-semibold tracking-tight">{$t('settings.title')}</h1>
	<p class="mt-1 text-sm text-muted-foreground">{$t('settings.subtitle')}</p>
</div>

<div class="flex flex-col gap-6 md:flex-row md:gap-8">
	<nav class="w-full shrink-0 md:w-48">
		<ul class="flex gap-1 overflow-x-auto pb-2 md:block md:space-y-0.5 md:overflow-visible md:pb-0">
			{#each visibleNavItems as item, i}
				{#if item.section && item.section !== visibleNavItems[i - 1]?.section}
					<li class="mb-1 hidden px-3 text-xs font-semibold uppercase tracking-wide text-muted-foreground/60 first:mt-0 mt-5 md:block">
						{item.section}
					</li>
				{/if}
				{@const active = isActive(item.href, item.exact)}
				<li class="shrink-0">
					<a
						href={item.href}
						class="block whitespace-nowrap rounded-md px-3 py-2 text-sm font-medium transition-colors
							{active
								? 'bg-accent text-accent-foreground'
								: 'text-muted-foreground hover:bg-accent/50 hover:text-accent-foreground'}"
					>
						{item.label}
					</a>
				</li>
			{/each}
		</ul>
	</nav>

	<div class="min-w-0 flex-1">
		{@render children()}
	</div>
</div>
