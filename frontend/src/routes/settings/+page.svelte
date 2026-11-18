<script lang="ts">
	import { base } from '$app/paths';
	import { currentUser } from '$lib/stores';

	let query = $state('');

	interface SettingCard {
		section: 'Personal Account' | 'Team Settings' | 'Instance Settings';
		title: string;
		description: string;
		href: string;
		adminOnly?: boolean;
	}

	const cards: SettingCard[] = [
		{
			section: 'Personal Account',
			title: 'Profile',
			description: 'Manage your name, avatar, timezone, language, and personal booking link.',
			href: `${base}/settings/profile`,
		},
		{
			section: 'Personal Account',
			title: 'Notifications',
			description: 'Configure your personal booking alerts and reminder preferences.',
			href: `${base}/settings/notifications`,
		},
		{
			section: 'Team Settings',
			title: 'Teams & Routing',
			description: 'Manage team members, team heads, routing priority, and team booking pages.',
			href: `${base}/teams`,
		},
		{
			section: 'Instance Settings',
			title: 'Branding',
			description: 'Customize instance logo, colors, and public booking page appearance.',
			href: `${base}/settings/branding`,
			adminOnly: true,
		},
		{
			section: 'Instance Settings',
			title: 'Email & RSVP',
			description: 'Configure SMTP / Resend delivery and built-in calendar RSVP tracking.',
			href: `${base}/settings/email`,
			adminOnly: true,
		},
		{
			section: 'Instance Settings',
			title: 'Google OAuth',
			description: 'Connect Google Workspace OAuth for Calendar and Google Meet.',
			href: `${base}/settings/google`,
			adminOnly: true,
		},
		{
			section: 'Instance Settings',
			title: 'Microsoft OAuth',
			description: 'Configure Microsoft Entra ID SSO, Outlook Calendar, and Teams.',
			href: `${base}/settings/microsoft`,
			adminOnly: true,
		},
		{
			section: 'Instance Settings',
			title: 'Zoom',
			description: 'Set up Server-to-Server or OAuth Zoom video conferencing.',
			href: `${base}/settings/zoom`,
			adminOnly: true,
		},
		{
			section: 'Instance Settings',
			title: 'Video',
			description: 'Configure default video providers and custom conferencing options.',
			href: `${base}/settings/video`,
			adminOnly: true,
		},
		{
			section: 'Instance Settings',
			title: 'Payments',
			description: 'Connect Stripe to accept paid bookings on event types.',
			href: `${base}/settings/payments`,
			adminOnly: true,
		},
		{
			section: 'Instance Settings',
			title: 'Storage',
			description: 'Configure S3 / object storage for file uploads and attachments.',
			href: `${base}/settings/storage`,
			adminOnly: true,
		},
		{
			section: 'Instance Settings',
			title: 'AI',
			description: 'Configure LLM providers for smart scheduling and assistant features.',
			href: `${base}/settings/ai`,
			adminOnly: true,
		},
		{
			section: 'Instance Settings',
			title: 'Tracking',
			description: 'Configure analytics, webhooks, and conversion tracking.',
			href: `${base}/settings/tracking`,
			adminOnly: true,
		},
		{
			section: 'Instance Settings',
			title: 'Members & Roles',
			description: 'Invite users, manage custom RBAC roles, and configure impersonation.',
			href: `${base}/settings/members`,
			adminOnly: true,
		},
	];

	const visibleCards = $derived.by(() => {
		const q = query.trim().toLowerCase();
		return cards.filter((c) => {
			if (c.adminOnly && !$currentUser?.permissions?.[c.href.endsWith('/members')?'members.read':c.href.endsWith('/branding')?'branding.read':'settings.read']) return false;
			if (!q) return true;
			return (
				c.title.toLowerCase().includes(q) ||
				c.description.toLowerCase().includes(q) ||
				c.section.toLowerCase().includes(q)
			);
		});
	});

	const sections = $derived(
		(['Personal Account', 'Team Settings', 'Instance Settings'] as const)
			.map((sec) => ({
				name: sec,
				items: visibleCards.filter((c) => c.section === sec),
			}))
			.filter((s) => s.items.length > 0),
	);
</script>

<div class="space-y-8">
	<div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
		<div>
			<h2 class="text-lg font-semibold">Settings Overview</h2>
			<p class="text-sm text-muted-foreground">Jump to personal, team, or instance-wide configuration.</p>
		</div>
		<input
			type="search"
			bind:value={query}
			placeholder="Search settings..."
			class="w-full rounded-lg border border-border bg-background px-3 py-2 text-sm outline-none focus:border-foreground/40 sm:w-64"
		/>
	</div>

	{#if sections.length === 0}
		<div class="rounded-xl border border-dashed border-border p-8 text-center text-sm text-muted-foreground">
			No settings match "{query}".
		</div>
	{:else}
		{#each sections as sec}
			<section class="space-y-3">
				<h3 class="text-xs font-semibold uppercase tracking-wider text-muted-foreground">{sec.name}</h3>
				<div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
					{#each sec.items as card}
						<a
							href={card.href}
							class="group rounded-xl border border-border bg-card p-4 transition-colors hover:border-foreground/30 hover:bg-accent/30"
						>
							<div class="flex items-center justify-between gap-2">
								<span class="text-sm font-semibold text-foreground group-hover:underline">{card.title}</span>
								<span class="text-xs text-muted-foreground">→</span>
							</div>
							<p class="mt-1.5 text-xs leading-relaxed text-muted-foreground">{card.description}</p>
						</a>
					{/each}
				</div>
			</section>
		{/each}
	{/if}
</div>
