<script lang="ts">
	import { ChevronLeft, ChevronRight } from '@lucide/svelte';
	import type { Snippet } from 'svelte';
	import IconButton from './IconButton.svelte';

	/** A sideways row with its heading, and arrows beside the heading when there is more than fits. */
	let { label, header, children, class: className = '' }: { label: string; header: Snippet; children: Snippet; class?: string } = $props();

	let row: HTMLDivElement;
	let back = $state(false);
	let forward = $state(false);

	function measure() {
		back = row.scrollLeft > 1;
		forward = row.scrollLeft + row.clientWidth < row.scrollWidth - 1;
	}
	function move(direction: 1 | -1) {
		const still = matchMedia('(prefers-reduced-motion: reduce)').matches;
		row.scrollBy({ left: direction * row.clientWidth * 0.8, behavior: still ? 'auto' : 'smooth' });
	}

	$effect(() => {
		// Re-measure when the row is resized or its items change.
		const size = new ResizeObserver(measure);
		const items = new MutationObserver(measure);
		size.observe(row);
		items.observe(row, { childList: true });
		measure();
		return () => {
			size.disconnect();
			items.disconnect();
		};
	});
</script>

<div class="flex items-start justify-between gap-3">
	<div class="min-w-0 flex-1">{@render header()}</div>
	{#if back || forward}
		<div class="-mt-2 flex shrink-0 gap-1">
			<IconButton icon={ChevronLeft} label="Scroll {label} back" disabled={!back} onclick={() => move(-1)} />
			<IconButton icon={ChevronRight} label="Scroll {label} forward" disabled={!forward} onclick={() => move(1)} />
		</div>
	{/if}
</div>
<div bind:this={row} onscroll={measure} class="shelf flex overflow-x-auto {className}">{@render children()}</div>
