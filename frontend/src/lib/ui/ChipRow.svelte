<script lang="ts">
	import type { Snippet } from 'svelte';

	/**
	 * Horizontally scrolling row of <Chip>s. `sticky` pins it under the safe area while the
	 * page scrolls (it bleeds to the screen edges inside the page gutters).
	 */
	let {
		sticky = true,
		label = 'Filters',
		class: className = '',
		children
	}: { sticky?: boolean; label?: string; class?: string; children: Snippet } = $props();
</script>

<div
	role="group"
	aria-label={label}
	class="z-20 -mx-4 bg-canvas/90 px-4 py-2 backdrop-blur-xl sm:-mx-6 sm:px-6 lg:-mx-10 lg:px-10 {sticky
		? 'sticky top-[env(safe-area-inset-top)]'
		: ''} {className}"
>
	<div class="shelf flex gap-2 overflow-x-auto">
		{@render children()}
	</div>
</div>
