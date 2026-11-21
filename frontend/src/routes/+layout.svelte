<script lang="ts">
	import { onMount, tick } from 'svelte';
	import { page } from '$app/state';
	import { base } from '$lib/paths';
	import '../app.css';
	import { api, type User } from '$lib/api';
	import { currentUser, authStatus, type AuthStatus } from '$lib/stores';
	import { prefs, prefsFromUser } from '$lib/prefs';
	import { theme, type ThemeMode } from '$lib/theme.svelte';
	import { t } from '$lib/i18n';
	import { Toaster } from '$lib/components/ui/sonner';
	import { toast } from 'svelte-sonner';
	import { buttonVariants } from '$lib/components/ui/button';
	import * as Dialog from '$lib/components/ui/dialog';
	import DemoBanner from '$lib/components/demo-banner.svelte';
	import QuickLinkPalette from '$lib/components/QuickLinkPalette.svelte';
	import { Button } from '$lib/components/ui/button';
	import Sun from '@lucide/svelte/icons/sun';
	import Moon from '@lucide/svelte/icons/moon';
	import Monitor from '@lucide/svelte/icons/monitor';
	import Search from '@lucide/svelte/icons/search';
	import type { Snippet } from 'svelte';

	let { children }: { children: Snippet } = $props();

	let checking = $state(true);
	let reportOpen = $state(false);
	let recordingsConfigured = $state(false);
	let version = $state('');
	// Mobile nav drawer. Only ever opened from the md:hidden header button, so it is
	// always false at desktop widths — which is what makes `inert` on <main> safe.
	let sidebarOpen = $state(false);
	let quickLinkOpen = $state(false);
	async function openQuickLinks() {
		sidebarOpen = false;
		await tick();
		quickLinkOpen = true;
	}
	let sidebarHovered = $state(false);
	const isCompactNav = $derived(
		page.route.id === '/event-types/[slug]' || (page.route.id?.startsWith('/settings') ?? false)
	);
	// Focus handling: opening moves focus into the drawer, closing returns it to the
	// trigger. Without this, opening leaves focus on the button the backdrop covers,
	// and closing hides the focused element (`visibility: hidden`), dropping focus to
	// <body> and losing the user's place.
	let sidebarEl: HTMLElement | null = $state(null);
	let menuButtonEl: HTMLButtonElement | null = $state(null);
	let drawerWasOpen = false;

	const ISSUES_URL = 'https://github.com/Calnode/calnode/issues';
	const NEW_ISSUE_URL = 'https://github.com/Calnode/calnode/issues/new/choose';
	const RELEASES_URL = 'https://github.com/Calnode/calnode/releases';

	const isLogin = $derived(page.route.id === '/login');
	const isPublicRoute = $derived(
		(page.route.id?.startsWith('/auth/') ?? false) ||
		page.route.id === '/login' ||
		page.route.id === '/claim' ||
		page.route.id === '/forgot-password' ||
		page.route.id === '/reset-password' ||
		page.route.id === '/invite/[token]'
	);

	const navItems = $derived([
		{
			id: 'home',
			section: $t('layout.nav.section.scheduling'),
			href: `${base}/`,
			label: $t('layout.nav.home'),
			adminOnly: false,
			exact: true,
			icon: `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 9l9-7 9 7v11a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/><polyline points="9 22 9 12 15 12 15 22"/></svg>`
		},
		{
			id: 'event-types',
			section: $t('layout.nav.section.scheduling'),
			href: `${base}/event-types`,
			label: $t('layout.nav.eventTypes'),
			adminOnly: false,
			icon: `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="18" rx="2" ry="2"/><line x1="16" y1="2" x2="16" y2="6"/><line x1="8" y1="2" x2="8" y2="6"/><line x1="3" y1="10" x2="21" y2="10"/></svg>`
		},
		{
			id: 'availability',
			section: $t('layout.nav.section.scheduling'),
			href: `${base}/availability`,
			label: $t('layout.nav.availability'),
			adminOnly: false,
			icon: `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/></svg>`
		},
		{
			id: 'bookings',
			section: $t('layout.nav.section.scheduling'),
			href: `${base}/bookings`,
			label: $t('layout.nav.bookings'),
			adminOnly: false,
			icon: `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="8" y1="6" x2="21" y2="6"/><line x1="8" y1="12" x2="21" y2="12"/><line x1="8" y1="18" x2="21" y2="18"/><line x1="3" y1="6" x2="3.01" y2="6"/><line x1="3" y1="12" x2="3.01" y2="12"/><line x1="3" y1="18" x2="3.01" y2="18"/></svg>`
		},
		{
			id: 'calendar',
			section: $t('layout.nav.section.scheduling'),
			href: `${base}/calendar`,
			label: $t('layout.nav.calendar'),
			adminOnly: false,
			icon: `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="18" rx="2" ry="2"/><line x1="16" y1="2" x2="16" y2="6"/><line x1="8" y1="2" x2="8" y2="6"/><line x1="3" y1="10" x2="21" y2="10"/><rect x="8" y="14" width="2" height="2"/><rect x="13" y="14" width="2" height="2"/></svg>`
		},
		{
			id: 'recordings',
			section: $t('layout.nav.section.team'),
			href: `${base}/recordings`,
			label: $t('layout.nav.recordings'),
			adminOnly: true,
			requiresFeature: 'recordings',
			icon: `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polygon points="23 7 16 12 23 17 23 7"/><rect x="1" y="5" width="15" height="14" rx="2" ry="2"/></svg>`
		},
		{
			id: 'members',
			section: $t('layout.nav.section.team'),
			href: `${base}/members`,
			label: $t('layout.nav.members'),
			adminOnly: true,
			icon: `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M23 21v-2a4 4 0 0 0-3-3.87"/><path d="M16 3.13a4 4 0 0 1 0 7.75"/></svg>`
		},
		{
			id: 'teams',
			section: $t('layout.nav.section.team'),
			href: `${base}/teams`,
			label: $t('layout.nav.teams'),
			adminOnly: true,
			icon: `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M14 19a6 6 0 0 0-12 0"/><circle cx="8" cy="9" r="4"/><path d="M22 19a6 6 0 0 0-6-6 4 4 0 1 0-3-7"/></svg>`
		},
		{
			id: 'api-keys',
			section: $t('layout.nav.section.developer'),
			href: `${base}/api-keys`,
			label: $t('layout.nav.apiKeys'),
			adminOnly: false,
			icon: `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 2l-2 2m-7.61 7.61a5.5 5.5 0 1 1-7.778 7.778 5.5 5.5 0 0 1 7.777-7.777zm0 0L15.5 7.5m0 0l3 3L22 7l-3-3m-3.5 3.5L19 4"/></svg>`
		},
		{
			id: 'connections',
			section: $t('layout.nav.section.developer'),
			href: `${base}/connections`,
			label: $t('layout.nav.connectedApps'),
			adminOnly: false,
			icon: `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 22v-5"/><path d="M9 8V2"/><path d="M15 8V2"/><path d="M18 8v5a4 4 0 0 1-4 4h-4a4 4 0 0 1-4-4V8Z"/></svg>`
		},
		{
			id: 'webhooks',
			section: $t('layout.nav.section.developer'),
			href: `${base}/webhooks`,
			label: $t('layout.nav.webhooks'),
			adminOnly: false,
			icon: `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9"/><path d="M13.73 21a2 2 0 0 1-3.46 0"/></svg>`
		},
		{
			id: 'settings',
			section: null,
			href: `${base}/settings`,
			label: $t('layout.nav.settings'),
			adminOnly: false,
			icon: `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/></svg>`
		}
	]);

	const navPermissions:Record<string,string>={'event-types':'event_types.read',availability:'availability.read',bookings:'bookings.read',members:'members.read',teams:'teams.read','api-keys':'api_keys.read',connections:'mcp.manage',webhooks:'webhooks.read',recordings:'recordings.read'};
 const navAllowed=(id:string,adminOnly?:boolean)=>{const permissions=$currentUser?.permissions;const permission=navPermissions[id];if(id==='event-types'&&permissions)return !!(permissions['event_types.read']||permissions['team_event_types.read']);if(permissions&&permission)return !!permissions[permission];return !adminOnly||!!$currentUser?.is_admin;};
 const visibleNavItems = $derived(
		navItems.filter(
			(item) =>
				navAllowed(item.id,item.adminOnly) &&
				!($authStatus.demo_mode && item.id === 'calendar') &&
				(item.requiresFeature !== 'recordings' || recordingsConfigured)
		)
	);

	// Close the mobile drawer whenever the route changes, so tapping a nav link does not
	// leave it open over the page it just navigated to.
	$effect(() => {
		void page.url.pathname;
		sidebarOpen = false;
	});

	// Move focus with the drawer. Effects run after the DOM flush, so the panel is
	// already visible/current when the first link is focused.
	$effect(() => {
		if (sidebarOpen) {
			drawerWasOpen = true;
			sidebarEl?.querySelector<HTMLElement>('a[href], button')?.focus();
		} else if (drawerWasOpen) {
			drawerWasOpen = false;
			menuButtonEl?.focus();
		}
	});

	onMount(async () => {
		theme.init();
		// Close the drawer if the viewport grows past md while it is open, so it cannot
		// linger as an active overlay over the desktop layout. Specified in rem, not px:
		// every `md:` class compiles to 48rem, which only equals 768px at the 16px
		// default font size — in px the two thresholds drift apart and this listener
		// would never fire for the band where the drawer is already unreachable.
		const desktop = window.matchMedia('(min-width: 48rem)');
		const onDesktop = (e: MediaQueryListEvent) => {
			if (e.matches) sidebarOpen = false;
		};
		desktop.addEventListener('change', onDesktop);

		if (isPublicRoute) {
			checking = false;
			return;
		}
		try {
			const me = await api.get<User>('/v1/users/me');
			currentUser.set(me);
			prefs.set(prefsFromUser(me));
			if (me.is_admin) {
				try {
					const lk = await api.get<{ configured: boolean }>('/v1/settings/livekit');
					recordingsConfigured = lk.configured;
				} catch {
					// Non-critical — Recordings just stays hidden from nav.
				}
			}
		} catch {
			window.location.href = '/admin/login';
			return;
		}
		try {
			authStatus.set(await api.get<AuthStatus>('/v1/auth/status'));
		} catch {
			// Non-critical — the demo banner and calendar/Zoom hiding just won't show.
		}
		try {
			const v = await api.get<{ version: string }>('/version');
			version = v.version;
		} catch {
			// Non-critical - the sidebar version link just won't show.
		}
		checking = false;
	});

	async function logout() {
		await fetch('/v1/auth/logout', { method: 'POST', credentials: 'same-origin' });
		window.location.href = '/admin/login';
	}

	function initials(name: string) {
		return name
			.split(' ')
			.map((p) => p[0])
			.join('')
			.toUpperCase()
			.slice(0, 2);
	}
</script>

<svelte:window
	onkeydown={(e) => {
		if (e.key === 'Escape' && sidebarOpen) sidebarOpen = false;
		if (!isPublicRoute && !checking && $currentUser && (e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') { e.preventDefault(); void openQuickLinks(); }
	}}
/>

<Toaster position="bottom-right" />

{#if isPublicRoute}
	<div class="fixed right-4 top-4 z-40">
		<div class="inline-flex items-center rounded-lg border bg-card/90 p-0.5 shadow-sm backdrop-blur-sm" role="radiogroup" aria-label="Theme">
			{#each (['light', 'dark', 'system'] as ThemeMode[]) as modeOption}
				<button
					type="button"
					role="radio"
					aria-checked={theme.mode === modeOption}
					onclick={() => theme.set(modeOption)}
					class="rounded-md px-2.5 py-1 text-xs font-medium capitalize transition-colors {theme.mode === modeOption ? 'bg-secondary text-secondary-foreground shadow-xs' : 'text-muted-foreground hover:text-foreground'}"
				>
					{modeOption}
				</button>
			{/each}
		</div>
	</div>
	{@render children()}
{:else if checking}
	<div class="flex h-full items-center justify-center text-sm text-muted-foreground">
		{$t('common.loading')}
	</div>
{:else}
	<div class="flex h-full flex-col">
	<QuickLinkPalette bind:open={quickLinkOpen} />
	{#if $authStatus.demo_mode}
		<DemoBanner />
	{/if}
	{#if $currentUser?.impersonating}
		<div class="flex items-center justify-between gap-3 bg-amber-500/15 border-b border-amber-500/30 px-4 py-2 text-xs text-amber-900 dark:text-amber-200">
			<span>
				Impersonating <strong>{$currentUser.name}</strong> ({$currentUser.email})
				{#if $currentUser.impersonator_email}
					— signed in as {$currentUser.impersonator_name || $currentUser.impersonator_email}
				{/if}
			</span>
			<button
				type="button"
				class="rounded bg-amber-600 px-2.5 py-1 text-xs font-medium text-white hover:bg-amber-700"
				onclick={async () => {
					await api.post('/v1/admin/impersonate/stop');
					window.location.reload();
				}}
			>
				Exit impersonation
			</button>
		</div>
	{/if}

	<!-- Mobile chrome (§226 phase 1): a compact header with the nav trigger. Hidden from md
	     up, where the sidebar is always visible instead. -->
	<header class="flex items-center gap-1 border-b border-sidebar-border bg-sidebar px-2 py-1.5 md:hidden">
		<button
			type="button"
			bind:this={menuButtonEl}
			class={buttonVariants({ variant: 'ghost', size: 'icon-sm' }) + ' min-h-11 min-w-11'}
			aria-label="Open navigation"
			aria-controls="app-sidebar"
			aria-expanded={sidebarOpen}
			onclick={() => (sidebarOpen = true)}
		>
			<svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
				<line x1="3" y1="6" x2="21" y2="6"/><line x1="3" y1="12" x2="21" y2="12"/><line x1="3" y1="18" x2="21" y2="18"/>
			</svg>
		</button>
		<p class="min-w-0 truncate text-sm font-medium text-sidebar-foreground">{$currentUser?.name ?? 'Calnode'}</p>
	</header>

	<div class="flex min-h-0 flex-1 overflow-hidden">
		<!-- Below md this is an off-canvas drawer; from md up it is the static sidebar.
		     `invisible` (not just translated) keeps the closed drawer out of the tab order. -->
		{#if sidebarOpen}
			<button
				type="button"
				class="fixed inset-0 z-40 bg-black/40 md:hidden"
				aria-label="Close navigation"
				onclick={() => (sidebarOpen = false)}
			></button>
		{/if}
		{#if isCompactNav}
			<div class="hidden md:block md:w-14 md:shrink-0" aria-hidden="true"></div>
		{/if}
		<aside
			id="app-sidebar"
			bind:this={sidebarEl}
			onmouseenter={() => { if (isCompactNav) sidebarHovered = true; }}
			onmouseleave={() => { sidebarHovered = false; }}
			class="fixed inset-y-0 left-0 z-50 flex w-72 max-w-[85vw] shrink-0 flex-col border-r border-sidebar-border bg-sidebar shadow-xl transition-all duration-[240ms] ease-[cubic-bezier(0.16,1,0.3,1)] md:max-w-none md:translate-x-0
				{isCompactNav
					? (sidebarHovered ? 'md:fixed md:inset-y-0 md:left-0 md:z-50 md:w-56 md:shadow-xl' : 'md:fixed md:inset-y-0 md:left-0 md:z-40 md:w-14 md:shadow-none')
					: 'md:static md:z-auto md:w-56 md:shadow-none'}
				{sidebarOpen ? 'translate-x-0' : '-translate-x-full invisible md:visible'}"
		>
			<!-- User section -->
			<a href="{base}/settings/profile" onclick={() => (sidebarOpen = false)} class="flex items-center gap-3 border-b border-sidebar-border px-3.5 py-3 hover:bg-sidebar-accent/60 transition-colors {isCompactNav && !sidebarHovered ? 'md:justify-center md:px-0' : ''}">
				<!-- Fixed-size clip container: the image fills it and is clipped by the
				     parent's overflow-hidden (not its own border-radius). A rounded,
				     object-cover image on its own composited layer gets mis-painted by
				     Chrome — a smeared tile over the sidebar — when the main panel
				     repaints on scroll; clipping via the parent box prevents that. -->
				<span class="flex h-7 w-7 shrink-0 items-center justify-center overflow-hidden rounded-full bg-primary text-xs font-semibold text-primary-foreground">
					{#if $currentUser?.avatar_url}
						<img src={$currentUser.avatar_url} alt={$currentUser.name} class="h-full w-full object-cover" />
					{:else}
						{initials($currentUser?.name ?? 'U')}
					{/if}
				</span>
				<div class="min-w-0 flex-1 {isCompactNav && !sidebarHovered ? 'md:hidden' : ''}">
					<p class="truncate text-sm font-medium text-sidebar-foreground">{$currentUser?.name ?? ''}</p>
				</div>
			</a>

			<Button type="button" variant="ghost" size="sm" class="mx-2 mt-2 justify-center gap-2 text-muted-foreground" onclick={openQuickLinks} aria-label={$t('quickLink.title')} title={$t('quickLink.title')}>
				<Search class="size-3.5 shrink-0" />
				<kbd class="text-xs {isCompactNav && !sidebarHovered ? 'md:hidden' : ''}">Ctrl / ⌘ K</kbd>
			</Button>
			<!-- Nav -->
			<nav class="min-h-0 flex-1 space-y-0.5 overflow-y-auto p-2">
				{#each visibleNavItems as item, i}
					{#if item.section && item.section !== visibleNavItems[i - 1]?.section}
						<p class="mb-1 px-2.5 text-xs font-semibold uppercase tracking-wide text-sidebar-foreground/40 first:mt-0 mt-4 {isCompactNav && !sidebarHovered ? 'md:hidden' : ''}">
							{item.section}
						</p>
					{:else if !item.section && visibleNavItems[i - 1]?.section}
						<div class="my-2 border-t border-sidebar-border"></div>
					{/if}
					{@const active = item.exact
						? page.url.pathname === item.href || page.url.pathname === base
						: page.url.pathname.startsWith(item.href)}
					<a
						href={item.href}
						title={item.label}
						aria-label={item.label}
						onclick={() => (sidebarOpen = false)}
						class="flex items-center gap-2.5 rounded-md px-2.5 py-2 text-sm font-medium transition-colors {isCompactNav && !sidebarHovered ? 'md:justify-center md:px-0' : ''}
							{active
								? 'bg-sidebar-accent text-sidebar-accent-foreground'
								: 'text-sidebar-foreground/70 hover:bg-sidebar-accent/60 hover:text-sidebar-accent-foreground'}"
					>
						<span class="shrink-0 {active ? 'opacity-100' : 'opacity-60'}">{@html item.icon}</span>
						<span class="truncate {isCompactNav && !sidebarHovered ? 'md:hidden' : ''}">{item.label}</span>
					</a>
				{/each}
			</nav>

			<!-- Footer -->
			<div class="border-t border-sidebar-border p-2 space-y-1">
				{#if $currentUser}
					{@const myHandle = $currentUser.handle}
					{@const myBookingUrl = myHandle ? `/u/${myHandle}` : `${base}/settings/profile`}
					<div class="flex items-center gap-1 {isCompactNav && !sidebarHovered ? 'md:justify-center' : ''}">
						<a
							href={myBookingUrl}
							target={myHandle ? '_blank' : undefined}
							rel="noopener noreferrer"
							title={myHandle ? $t('layout.openBookingPage') : $t('layout.setupBookingPage')}
							class="flex min-w-0 flex-1 items-center gap-2.5 rounded-md px-2.5 py-1.5 text-xs font-medium text-sidebar-foreground/75 transition-colors hover:bg-sidebar-accent/60 hover:text-sidebar-foreground {isCompactNav && !sidebarHovered ? 'md:flex-none md:justify-center md:px-2' : ''}"
						>
							<svg xmlns="http://www.w3.org/2000/svg" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="shrink-0">
								<path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/><polyline points="15 3 21 3 21 9"/><line x1="10" y1="14" x2="21" y2="3"/>
							</svg>
							<span class="truncate {isCompactNav && !sidebarHovered ? 'md:hidden' : ''}">{myHandle ? $t('layout.bookingPage') : $t('layout.setupBookingPage')}</span>
						</a>
						{#if myHandle}
						<Button variant="ghost" size="icon"
							type="button"
							title={$t('layout.copyBookingPage')}
							aria-label={$t('layout.copyBookingPage')}
							onclick={async () => {
								const full = `${window.location.origin}${myBookingUrl}`;
								try {
									await navigator.clipboard.writeText(full);
									toast.success($t('common.copied'));
								} catch {
									toast.error($t('common.copyClipboardError'));
								}
							}}
							class="shrink-0 rounded-md p-1.5 text-sidebar-foreground/60 transition-colors hover:bg-sidebar-accent/60 hover:text-sidebar-foreground {isCompactNav && !sidebarHovered ? 'md:hidden' : ''}"
						>
							<svg xmlns="http://www.w3.org/2000/svg" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/><path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/></svg>
						</Button>
						{/if}
					</div>
				{/if}

				{#if isCompactNav}
					<Button type="button" variant="ghost" size="icon" title="Theme: {theme.mode}. Change theme" aria-label="Theme: {theme.mode}. Change theme" onclick={() => theme.set(theme.mode === 'light' ? 'dark' : theme.mode === 'dark' ? 'system' : 'light')} class="hidden w-full text-sidebar-foreground/70 md:flex">
						{#if theme.mode === 'light'}<Sun class="size-4" />{:else if theme.mode === 'dark'}<Moon class="size-4" />{:else}<Monitor class="size-4" />{/if}
					</Button>
				{/if}
				<div class="mb-1 px-1 {isCompactNav ? 'md:hidden' : ''}" role="radiogroup" aria-label="Theme">
					<div class="grid grid-cols-3 gap-0.5 rounded-md border border-sidebar-border bg-sidebar-accent/40 p-0.5">
						{#each (['light', 'dark', 'system'] as ThemeMode[]) as modeOption}
							<button
								type="button"
								role="radio"
								aria-checked={theme.mode === modeOption}
								onclick={() => theme.set(modeOption)}
								class="flex items-center justify-center gap-1 rounded px-1.5 py-1 text-[11px] font-medium capitalize transition-colors
									{theme.mode === modeOption
										? 'bg-background text-foreground shadow-xs'
										: 'text-sidebar-foreground/60 hover:text-sidebar-foreground'}"
							>
								{#if modeOption === 'light'}
									<svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="4"/><path d="M12 2v2"/><path d="M12 20v2"/><path d="m4.93 4.93 1.41 1.41"/><path d="m17.66 17.66 1.41 1.41"/><path d="M2 12h2"/><path d="M20 12h2"/><path d="m6.34 17.66-1.41 1.41"/><path d="m19.07 4.93-1.41 1.41"/></svg>
								{:else if modeOption === 'dark'}
									<svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 3a6 6 0 0 0 9 9 9 9 0 1 1-9-9Z"/></svg>
								{:else}
									<svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect width="20" height="14" x="2" y="3" rx="2"/><line x1="8" x2="16" y1="21" y2="21"/><line x1="12" x2="12" y1="17" y2="21"/></svg>
								{/if}
								<span>{modeOption}</span>
							</button>
						{/each}
					</div>
				</div>
				<button
					onclick={() => {
						sidebarOpen = false;
						reportOpen = true;
					}}
					title={$t('layout.reportIssue')}
					class="flex w-full items-center gap-2.5 rounded-md px-2.5 py-1.5 text-xs font-medium text-sidebar-foreground/45 transition-colors hover:bg-sidebar-accent/60 hover:text-sidebar-foreground/70 {isCompactNav && !sidebarHovered ? 'md:justify-center md:px-0' : ''}"
				>
					<svg xmlns="http://www.w3.org/2000/svg" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="shrink-0">
						<path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/>
					</svg>
					<span class="truncate {isCompactNav && !sidebarHovered ? 'md:hidden' : ''}">{$t('layout.reportIssue')}</span>
				</button>
				<button
					onclick={logout}
					title={$t('layout.signOut')}
					class="flex w-full items-center gap-2.5 rounded-md px-2.5 py-2 text-sm font-medium text-sidebar-foreground/60 transition-colors hover:bg-sidebar-accent/60 hover:text-sidebar-accent-foreground {isCompactNav && !sidebarHovered ? 'md:justify-center md:px-0' : ''}"
				>
					<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="shrink-0">
						<path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/>
						<polyline points="16 17 21 12 16 7"/>
						<line x1="21" y1="12" x2="9" y2="12"/>
					</svg>
					<span class="truncate {isCompactNav && !sidebarHovered ? 'md:hidden' : ''}">{$t('layout.signOut')}</span>
				</button>
			</div>
		</aside>

		<!-- Main content -->
		<main class="min-w-0 flex-1 overflow-y-auto bg-background" inert={sidebarOpen}>
			<div class="w-full px-4 py-4 sm:px-6 sm:py-6 lg:px-8 lg:py-6">
				{@render children()}
			</div>
		</main>
	</div>
	</div>

	<Dialog.Root bind:open={reportOpen}>
		<Dialog.Content class="max-w-md">
			<Dialog.Header>
				<Dialog.Title>{$t('layout.reportDialog.title')}</Dialog.Title>
				<Dialog.Description>
					{$t('layout.reportDialog.description')}
				</Dialog.Description>
			</Dialog.Header>
			<Dialog.Footer class="gap-2 sm:justify-between">
				<a
					href={ISSUES_URL}
					target="_blank"
					rel="noopener noreferrer"
					onclick={() => (reportOpen = false)}
					class={buttonVariants({ variant: 'outline' })}
				>
					{$t('layout.reportDialog.searchIssues')}
				</a>
				<a
					href={NEW_ISSUE_URL}
					target="_blank"
					rel="noopener noreferrer"
					onclick={() => (reportOpen = false)}
					class={buttonVariants()}
				>
					{$t('layout.reportDialog.reportBug')}
				</a>
			</Dialog.Footer>
		</Dialog.Content>
	</Dialog.Root>
{/if}
