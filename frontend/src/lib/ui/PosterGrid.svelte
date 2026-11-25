<script lang="ts">
	import type { Snippet } from 'svelte';

	/**
	 * Responsive grid for <PosterCard>s: 3 columns at 375px, more as space allows.
	 * `min` is the minimum tile width in px (default 104).
	 */
	let { min = 104, children, class: className = '' }: { min?: number; children: Snippet; class?: string } = $props();
</script>

<div
	class="grid gap-x-3 gap-y-5 sm:gap-x-4 {className}"
	style="grid-template-columns: repeat(auto-fill, minmax({min}px, 1fr))"
>
	{@render children()}
</div>
