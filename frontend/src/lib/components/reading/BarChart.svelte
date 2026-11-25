<script lang="ts">
	/**
	 * One-series bar chart (single y axis, thin bars, 4px rounded tops on a shared baseline).
	 * Labels: the peak is labelled, the y axis shows only its maximum, the x axis shows a few ticks.
	 * Hover/tap shows a tooltip; a visually hidden table carries the same numbers for screen readers.
	 */
	import type { Bar } from './types.ts';
	let {
		bars,
		accent = 'var(--color-type-book)',
		unit,
		title,
		height = 120,
		tickEvery = 1
	}: { bars: Bar[]; accent?: string; unit: string; title: string; height?: number; tickEvery?: number } = $props();

	const max = $derived(Math.max(1, ...bars.map((b) => b.value)));
	const peak = $derived(bars.reduce((best, b, i) => (b.value > (bars[best]?.value ?? 0) ? i : best), 0));
	let active = $state<number | null>(null);
</script>

<figure class="relative m-0" aria-label={title}>
	<div class="relative pl-9">
		<span class="absolute top-0 left-0 w-8 text-right text-[11px] leading-none text-faint tabular-nums"
			>{max.toLocaleString()}</span
		>
		<span class="absolute bottom-0 left-0 w-8 text-right text-[11px] leading-none text-faint tabular-nums">0</span>
		<div
			class="relative flex items-end gap-[3px] border-b border-line-strong"
			style="height: {height}px"
			role="presentation"
		>
			<div class="pointer-events-none absolute inset-x-0 top-0 border-t border-dashed border-line"></div>
			{#each bars as bar, i (i)}
				<button
					type="button"
					class="group relative flex h-full min-w-0 flex-1 items-end justify-center"
					aria-label="{bar.label}: {bar.value.toLocaleString()} {unit}"
					onpointerenter={() => (active = i)}
					onpointerleave={() => (active = null)}
					onfocus={() => (active = i)}
					onblur={() => (active = null)}
					onclick={() => (active = active === i ? null : i)}
				>
					<span
						class="block w-full max-w-4 rounded-t-[4px] transition-opacity {active !== null && active !== i ? 'opacity-45' : ''}"
						style="height: {bar.value ? Math.max(3, (bar.value / max) * 100) : 0}%; background: {accent}"
					></span>
				</button>
			{/each}
			{#if active !== null && bars[active]}
				<div
					class="pointer-events-none absolute bottom-full z-10 mb-1 -translate-x-1/2 rounded-md border border-line-strong bg-raised px-2 py-1 text-[12px] whitespace-nowrap shadow-lg"
					style="left: clamp(60px, {((active + 0.5) / bars.length) * 100}%, calc(100% - 60px))"
				>
					<span class="text-muted">{bars[active].label}</span>
					<span class="ml-1 font-semibold tabular-nums">{bars[active].value.toLocaleString()}</span>
					<span class="text-muted">{unit}</span>
				</div>
			{/if}
		</div>
		<div class="mt-1.5 flex gap-[3px] text-[10px] text-faint">
			{#each bars as bar, i (i)}
				<span class="min-w-0 flex-1 truncate text-center {i === peak && bar.value ? 'font-semibold text-muted' : ''}">
					{i % tickEvery === 0 ? bar.tick : ''}
				</span>
			{/each}
		</div>
	</div>
	<table class="sr-only">
		<caption>{title}</caption>
		<thead><tr><th>Period</th><th>{unit}</th></tr></thead>
		<tbody>
			{#each bars as bar, i (i)}<tr><th>{bar.label}</th><td>{bar.value}</td></tr>{/each}
		</tbody>
	</table>
</figure>
