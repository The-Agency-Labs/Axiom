<script lang="ts">
	/** Twelve monthly bars for one series, with a hover/tap tooltip per bar. */
	let {
		values,
		accent,
		unit,
		label,
		class: className = ''
	}: { values: number[]; accent: string; unit: string; label: string; class?: string } = $props();

	const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
	const max = $derived(Math.max(1, ...values));
	const peak = $derived(values.indexOf(Math.max(...values)));
	let hover = $state<number | null>(null);
</script>

<figure class="relative {className}" aria-label={label}>
	<div class="flex h-24 items-end gap-1" role="list">
		{#each values as value, i (i)}
			<div
				role="listitem"
				class="group relative flex h-full flex-1 items-end"
				onpointerenter={() => (hover = i)}
				onpointerleave={() => (hover = null)}
				aria-label="{MONTHS[i]}: {value.toLocaleString()} {unit}"
			>
				<div
					class="w-full rounded-t-[4px] transition-opacity {hover !== null && hover !== i ? 'opacity-50' : ''}"
					style="height: {value ? Math.max(4, (value / max) * 100) : 0}%; background: {accent}"
				></div>
				{#if !value}
					<div class="absolute bottom-0 h-px w-full bg-line-strong"></div>
				{/if}
			</div>
		{/each}
	</div>
	<div class="mt-1.5 flex gap-1 text-[0.75rem] text-faint">
		{#each MONTHS as month, i (month)}
			<span class="flex-1 text-center {i === peak && values[peak] ? 'text-muted' : ''}">{month[0]}</span>
		{/each}
	</div>
	{#if hover !== null}
		<div
			class="pointer-events-none absolute -top-8 z-10 -translate-x-1/2 rounded-md border border-line bg-raised px-2 py-1 text-[0.75rem] whitespace-nowrap shadow-lg"
			style="left: {((hover + 0.5) / 12) * 100}%"
		>
			<span class="text-muted">{MONTHS[hover]}</span>
			<span class="ml-1 font-semibold tabular-nums">{values[hover].toLocaleString()}</span>
			<span class="text-muted">{unit}</span>
		</div>
	{/if}
	<div class="sr-only">
	<table>
		<caption>{label}</caption>
		<tbody>
			{#each values as value, i (i)}<tr><th>{MONTHS[i]}</th><td>{value}</td></tr>{/each}
		</tbody>
	</table>
	</div>
</figure>
