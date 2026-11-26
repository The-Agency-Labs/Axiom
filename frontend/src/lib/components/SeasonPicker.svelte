<script lang="ts">
	import { ChevronDown } from '@lucide/svelte';

	export interface SeasonRef {
		season: string;
		year: number;
		label: string;
	}

	/** The season as a title that opens a grid of years by season. */
	let {
		seasons,
		value,
		current,
		onpick
	}: { seasons: SeasonRef[]; value: SeasonRef; current: SeasonRef | null; onpick: (s: SeasonRef) => void } = $props();

	const COLUMNS = [
		{ season: 'winter', months: 'Jan–Mar' },
		{ season: 'spring', months: 'Apr–Jun' },
		{ season: 'summer', months: 'Jul–Sep' },
		{ season: 'fall', months: 'Oct–Dec' }
	];
	const years = $derived([...new Set(seasons.map((s) => s.year))]);
	const find = (year: number, season: string) => seasons.find((s) => s.year === year && s.season === season);
	const name = (season: string) => season[0].toUpperCase() + season.slice(1);

	let open = $state(false);
	let root: HTMLDivElement;
	let trigger: HTMLButtonElement;

	function pick(s: SeasonRef) {
		onpick(s);
		close();
	}
	function close() {
		open = false;
		trigger.focus({ preventScroll: true });
	}
	function outside(e: Event) {
		if (open && !root.contains(e.target as Node)) open = false;
	}
	function key(e: KeyboardEvent) {
		if (open && e.key === 'Escape') close();
	}
</script>

<svelte:window onpointerdown={outside} onkeydown={key} />

<div bind:this={root} class="relative">
	<h1>
		<button
			bind:this={trigger}
			type="button"
			class="-ml-1 flex min-h-11 items-center gap-2 rounded-lg px-1 text-2xl font-semibold tracking-tight whitespace-nowrap transition hover:bg-ink/5 sm:text-3xl"
			aria-haspopup="dialog"
			aria-expanded={open}
			onclick={() => (open = !open)}
		>
			{value.label}
			<ChevronDown class="size-5 text-muted transition {open ? 'rotate-180' : ''}" />
		</button>
	</h1>

	{#if open}
		<div
			role="dialog"
			aria-label="Choose a season"
			class="absolute top-full left-0 z-30 mt-2 max-h-[65vh] w-[min(23rem,calc(100vw-2rem))] overflow-y-auto rounded-2xl border border-line bg-surface p-3 shadow-2xl"
		>
			<div class="grid grid-cols-[2.75rem_repeat(4,minmax(0,1fr))] items-center gap-1.5">
				<span></span>
				{#each COLUMNS as c (c.season)}<span class="text-center text-[0.6875rem] text-faint">{c.months}</span>{/each}
				{#each years as year (year)}
					<span class="text-xs font-semibold tabular-nums">{year}</span>
					{#each COLUMNS as c (c.season)}
						{@const s = find(year, c.season)}
						{@const picked = year === value.year && c.season === value.season}
						{@const airing = current?.year === year && current?.season === c.season}
						<button
							type="button"
							class="relative h-10 rounded-lg border text-xs font-medium transition disabled:opacity-30 {picked
								? 'border-ink bg-ink font-semibold text-canvas'
								: 'border-line bg-raised enabled:hover:border-line-strong'}"
							disabled={!s}
							aria-pressed={picked}
							aria-label="{name(c.season)} {year}{airing ? ', airing now' : ''}"
							onclick={() => s && pick(s)}
						>
							{name(c.season)}
							{#if airing}<span class="absolute top-1 right-1 size-1.5 rounded-full bg-success"></span>{/if}
						</button>
					{/each}
				{/each}
			</div>
			<div class="mt-3 flex items-center justify-between gap-3 border-t border-line pt-1">
				<span class="flex items-center gap-1.5 text-xs text-faint"><span class="size-1.5 rounded-full bg-success"></span>Airing now</span>
				<button
					type="button"
					class="min-h-10 rounded-lg px-2 text-xs font-semibold transition hover:bg-ink/5 disabled:opacity-40"
					disabled={!current}
					onclick={() => current && pick(find(current.year, current.season) ?? current)}
				>
					Jump to this season
				</button>
			</div>
		</div>
	{/if}
</div>
