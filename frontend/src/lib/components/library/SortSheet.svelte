<script lang="ts">
	import { Check } from '@lucide/svelte';
	import { SORTS } from '#lib/media.ts';
	import { Sheet } from '#lib/ui/index.ts';

	let {
		open = $bindable(false),
		value = $bindable('activity'),
		onpick
	}: { open: boolean; value: string; onpick?: (value: string) => void } = $props();
</script>

<Sheet bind:open title="Sort by">
	<ul class="-mx-2 pb-2" role="radiogroup" aria-label="Sort by">
		{#each SORTS as [id, label] (id)}
			<li>
				<button
					type="button"
					role="radio"
					aria-checked={value === id}
					class="flex min-h-12 w-full items-center justify-between rounded-xl px-3 text-left text-[15px] hover:bg-ink/5 {value === id
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
