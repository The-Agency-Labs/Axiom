<script lang="ts">
	import { ArrowDown, LoaderCircle } from '@lucide/svelte';
	import type { Snippet } from 'svelte';

	/**
	 * Pull-to-refresh for touch screens: pull down from the top of the page, release past the threshold
	 * and `onrefresh` runs (return a promise; the spinner stays until it settles). No effect with a mouse
	 * or when the page is scrolled.
	 *
	 *   <PullToRefresh onrefresh={load}>...page content...</PullToRefresh>
	 */
	let { onrefresh, children }: { onrefresh: () => Promise<unknown> | unknown; children: Snippet } = $props();

	const THRESHOLD = 72;
	let pull = $state(0);
	let refreshing = $state(false);
	let startY = 0;
	let tracking = false;
	let root: HTMLDivElement;

	function start(e: TouchEvent) {
		if (refreshing || window.scrollY > 0 || document.body.style.overflow === 'hidden') return;
		startY = e.touches[0].clientY;
		tracking = true;
	}
	function move(e: TouchEvent) {
		if (!tracking) return;
		const dy = e.touches[0].clientY - startY;
		if (dy <= 0 || window.scrollY > 0) {
			pull = 0;
			return;
		}
		pull = Math.min(dy * 0.5, THRESHOLD * 1.5);
		if (e.cancelable && dy > 8) e.preventDefault();
	}
	async function end() {
		if (!tracking) return;
		tracking = false;
		if (pull >= THRESHOLD) {
			refreshing = true;
			pull = THRESHOLD * 0.7;
			try {
				await onrefresh();
			} finally {
				refreshing = false;
				pull = 0;
			}
		} else pull = 0;
	}

	$effect(() => {
		// touchmove must be non-passive to cancel the native overscroll while pulling.
		root.addEventListener('touchmove', move, { passive: false });
		return () => root.removeEventListener('touchmove', move);
	});
</script>

<div bind:this={root} role="presentation" ontouchstart={start} ontouchend={end} ontouchcancel={end}>
	<div
		class="pointer-events-none flex items-end justify-center overflow-hidden text-muted {pull === 0 ? 'transition-[height] duration-200' : ''}"
		style="height: {pull}px"
		aria-hidden={!refreshing}
	>
		<span class="mb-2 grid size-8 place-items-center rounded-full bg-raised ring-1 ring-line">
			{#if refreshing}
				<LoaderCircle class="size-4 animate-spin" />
			{:else}
				<ArrowDown class="size-4 transition-transform {pull >= THRESHOLD ? 'rotate-180' : ''}" />
			{/if}
		</span>
	</div>
	{@render children()}
</div>
