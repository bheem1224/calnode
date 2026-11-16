<script lang="ts">
	import { Popover } from 'bits-ui';
	import { Button } from '$lib/components/ui/button';

	let {
		start = $bindable(''),
		end = $bindable(''),
		placeholder = 'Select date range'
	}: {
		start?: string;
		end?: string;
		placeholder?: string;
	} = $props();

	let open = $state(false);
	let viewDate = $state(new Date());
	let pickingEnd = $state(false);
	let hoverIso = $state('');

	$effect(() => {
		if (start) {
			const [y, m] = start.split('-').map(Number);
			if (y && m) viewDate = new Date(y, m - 1, 1);
		}
	});

	const monthLabel = $derived(
		viewDate.toLocaleDateString('en-US', { month: 'long', year: 'numeric' })
	);

	const cells = $derived.by(() => {
		const y = viewDate.getFullYear();
		const m = viewDate.getMonth();
		const firstDow = new Date(y, m, 1).getDay();
		const daysInMonth = new Date(y, m + 1, 0).getDate();
		const out: { day: number | null; iso: string }[] = [];
		for (let i = 0; i < firstDow; i++) out.push({ day: null, iso: '' });
		for (let d = 1; d <= daysInMonth; d++) {
			out.push({
				day: d,
				iso: `${y}-${String(m + 1).padStart(2, '0')}-${String(d).padStart(2, '0')}`
			});
		}
		return out;
	});

	function shiftMonth(delta: number) {
		viewDate = new Date(viewDate.getFullYear(), viewDate.getMonth() + delta, 1);
	}

	function clickDay(iso: string) {
		if (!iso) return;
		if (!pickingEnd || !start) {
			start = iso;
			end = iso;
			pickingEnd = true;
		} else {
			if (iso < start) {
				end = start;
				start = iso;
			} else {
				end = iso;
			}
			pickingEnd = false;
			open = false;
		}
	}

	function fmtShort(iso: string): string {
		if (!iso) return '';
		const [y, m, d] = iso.split('-').map(Number);
		return new Date(y, m - 1, d).toLocaleDateString('en-US', {
			month: 'short',
			day: 'numeric',
			year: 'numeric'
		});
	}

	const displayLabel = $derived(
		start && end
			? start === end
				? fmtShort(start)
				: `${fmtShort(start)} – ${fmtShort(end)}`
			: placeholder
	);

	function isSelected(iso: string): boolean {
		return !!iso && (iso === start || iso === end);
	}

	function isInRange(iso: string): boolean {
		if (!iso || !start) return false;
		const hi = pickingEnd && hoverIso ? (hoverIso >= start ? hoverIso : start) : end;
		const lo = pickingEnd && hoverIso && hoverIso < start ? hoverIso : start;
		return !!hi && iso > lo && iso < hi;
	}
</script>

<Popover.Root bind:open>
	<Popover.Trigger
		class="flex h-9 w-full items-center justify-between gap-2 rounded-md border bg-background px-3 text-sm hover:bg-accent/40"
	>
		<span class="flex items-center gap-2 truncate">
			<svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="shrink-0 text-muted-foreground"><rect x="3" y="4" width="18" height="18" rx="2" ry="2"/><line x1="16" y1="2" x2="16" y2="6"/><line x1="8" y1="2" x2="8" y2="6"/><line x1="3" y1="10" x2="21" y2="10"/></svg>
			<span class={start ? '' : 'text-muted-foreground'}>{displayLabel}</span>
		</span>
	</Popover.Trigger>
	<Popover.Content class="z-50 w-72 rounded-lg border bg-popover p-3 text-popover-foreground shadow-lg" sideOffset={4}>
		<div class="mb-2 flex items-center justify-between">
			<Button type="button" variant="ghost" size="icon" class="h-7 w-7" onclick={() => shiftMonth(-1)}>
				‹
			</Button>
			<span class="text-xs font-semibold">{monthLabel}</span>
			<Button type="button" variant="ghost" size="icon" class="h-7 w-7" onclick={() => shiftMonth(1)}>
				›
			</Button>
		</div>
		<div class="mb-1 grid grid-cols-7 text-center text-[11px] font-medium text-muted-foreground">
			<span>Su</span><span>Mo</span><span>Tu</span><span>We</span><span>Th</span><span>Fr</span><span>Sa</span>
		</div>
		<div class="grid grid-cols-7 gap-0.5 text-center text-xs">
			{#each cells as cell}
				{#if cell.day === null}
					<div class="h-8"></div>
				{:else}
					{@const sel = isSelected(cell.iso)}
					{@const inR = isInRange(cell.iso)}
					<button
						type="button"
						class="h-8 w-full rounded-md text-xs transition-colors {sel
							? 'bg-primary text-primary-foreground font-semibold'
							: inR
								? 'bg-primary/15 text-foreground'
								: 'hover:bg-accent'}"
						onmouseenter={() => { hoverIso = cell.iso; }}
						onclick={() => clickDay(cell.iso)}
					>
						{cell.day}
					</button>
				{/if}
			{/each}
		</div>
		<div class="mt-2 flex items-center justify-between border-t pt-2 text-[11px] text-muted-foreground">
			<span>{pickingEnd ? 'Click end date' : 'Click start date'}</span>
			{#if start}
				<button type="button" class="hover:text-foreground" onclick={() => { start = ''; end = ''; pickingEnd = false; }}>
					Clear
				</button>
			{/if}
		</div>
	</Popover.Content>
</Popover.Root>
