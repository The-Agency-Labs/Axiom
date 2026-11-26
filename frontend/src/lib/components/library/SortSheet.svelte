<script lang="ts">
	import { Check } from '@lucide/svelte';
	import { NO_STATUS, SORTS, yearView } from '#lib/media.ts';
	import { Sheet } from '#lib/ui/index.ts';

	let {
		open = $bindable(false),
		value = $bindable('activity'),
		kind = '',
		onpick
	}: { open: boolean; value: string; kind?: string; onpick?: (value: string) => void } = $props();
	// "Year" means something different per kind; kinds without status have no start/finish dates or progress.
	const sorts = $derived(
		SORTS.filter(([id]) => !NO_STATUS.has(kind) || !['progress', 'start_date', 'end_date'].includes(id)).map(
			([id, label]) => [id, id === 'year' ? yearView(kind).label : label] as [string, string]
		)
	);
</script>

<Sheet bind:open title="Sort by">
	<ul class="-mx-2 pb-2" role="radiogroup" aria-label="Sort by">
		{#each sorts as [id, label] (id)}
			<li>
				<button
					type="button"
					role="radio"
					aria-checked={value === id}
					class="flex min-h-12 w-full items-center justify-between rounded-xl px-3 text-left text-[0.9375rem] hover:bg-ink/5 {value === id
						? 'font-semibold'
						: ''}"
					onclick={() => {
						value = id;
						onpick?.(id);
						open = false;
					}}
				>
					{label}
					{#if value === id}<Check class="size-5 text-brand" />{/if}
				</button>
			</li>
		{/each}
	</ul>
</Sheet>
