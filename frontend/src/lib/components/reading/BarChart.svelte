<script lang="ts">
	/**
	 * One-series bar chart (single y axis, thin bars, 4px rounded tops on a shared baseline).
	 * Labels: the y axis runs to a round number with its halfway line; the x axis shows the ticks given.
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

	/** The axis ends on a round number at or above the tallest bar. */
	const max = $derived.by(() => {
		const top = Math.max(1, ...bars.map((b) => b.value));
		const step = 10 ** Math.floor(Math.log10(top));
		return [1, 2, 4, 5, 6, 8, 10].map((n) => n * step).find((n) => n >= top) ?? top;
	});
	let active = $state<number | null>(null);
</script>

<figure class="relative m-0" aria-label={title}>
	<div class="relative pt-2 pl-10">
		<div
			class="relative flex items-end gap-[3px] border-b border-line-strong"
			style="height: {height}px"
			role="presentation"
		>
			<div class="pointer-events-none absolute inset-x-0 top-0 border-t border-dashed border-line"></div>
			{#if max % 2 === 0}
				<div class="pointer-events-none absolute inset-x-0 top-1/2 border-t border-dashed border-line"></div>
				<span class="absolute top-1/2 right-full mr-1 -translate-y-1/2 text-[0.75rem] leading-none text-faint tabular-nums">{(max / 2).toLocaleString()}</span>
			{/if}
			<span class="absolute top-0 right-full mr-1 -translate-y-1/2 text-[0.75rem] leading-none text-faint tabular-nums">{max.toLocaleString()}</span>
			<span class="absolute right-full bottom-0 mr-1 translate-y-1/2 text-[0.75rem] leading-none text-faint tabular-nums">0</span>
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
					class="pointer-events-none absolute bottom-full z-10 mb-1 -translate-x-1/2 rounded-md border border-line-strong bg-raised px-2 py-1 text-[0.75rem] whitespace-nowrap shadow-lg"
					style="left: clamp(60px, {((active + 0.5) / bars.length) * 100}%, calc(100% - 60px))"
				>
					<span class="text-muted">{bars[active].label}</span>
					<span class="ml-1 font-semibold tabular-nums">{bars[active].value.toLocaleString()}</span>
					<span class="text-muted">{unit}</span>
				</div>
			{/if}
		</div>
		<div class="mt-1.5 flex gap-[3px] text-[0.75rem] text-faint">
			{#each bars as bar, i (i)}
				<span class="flex min-w-0 flex-1 justify-center whitespace-nowrap {bar.wideOnly ? 'max-sm:invisible' : ''}">
					{i % tickEvery === 0 ? bar.tick : ''}
				</span>
			{/each}
		</div>
	</div>
	<div class="sr-only">
	<table>
		<caption>{title}</caption>
		<thead><tr><th>Period</th><th>{unit}</th></tr></thead>
		<tbody>
			{#each bars as bar, i (i)}<tr><th>{bar.label}</th><td>{bar.value}</td></tr>{/each}
		</tbody>
	</table>
	</div>
</figure>
