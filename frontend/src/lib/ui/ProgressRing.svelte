<script lang="ts">
	import type { Snippet } from 'svelte';

	/** Circular progress. Centre content (e.g. a percentage or an icon) goes in the children snippet. */
	let {
		percent,
		size = 56,
		stroke = 5,
		accent = 'var(--color-brand)',
		label = 'Progress',
		children
	}: {
		percent: number | null;
		size?: number;
		stroke?: number;
		accent?: string;
		label?: string;
		children?: Snippet;
	} = $props();

	const value = $derived(Math.max(0, Math.min(100, percent ?? 0)));
	const radius = $derived((size - stroke) / 2);
	const circumference = $derived(2 * Math.PI * radius);
</script>

<div
	role="progressbar"
	aria-label={label}
	aria-valuemin="0"
	aria-valuemax="100"
	aria-valuenow={Math.round(value)}
	class="relative inline-grid shrink-0 place-items-center"
	style="width: {size}px; height: {size}px"
>
	<svg width={size} height={size} class="-rotate-90" aria-hidden="true">
		<circle cx={size / 2} cy={size / 2} r={radius} fill="none" stroke="currentColor" stroke-opacity="0.12" stroke-width={stroke} />
		<circle
			cx={size / 2}
			cy={size / 2}
			r={radius}
			fill="none"
			stroke={accent}
			stroke-width={stroke}
			stroke-linecap="round"
			stroke-dasharray={circumference}
			stroke-dashoffset={circumference * (1 - value / 100)}
			class="transition-[stroke-dashoffset] duration-500 ease-out"
		/>
	</svg>
	<div class="absolute inset-0 grid place-items-center text-xs font-semibold tabular-nums">
		{#if children}{@render children()}{:else}{Math.round(value)}%{/if}
	</div>
</div>
