<script lang="ts">
	import { STATUS_COLOR } from '#lib/media.ts';
	import type { Status } from '#lib/types.ts';
	import type { Snippet } from 'svelte';

	/**
	 * Entry status as a coloured dot + text. `compact` is the small caption style used under posters;
	 * children (if any) replace the status label (e.g. "42% - 12/28 ep").
	 */
	let {
		status,
		compact = false,
		class: className = '',
		children
	}: { status: Status; compact?: boolean; class?: string; children?: Snippet | string } = $props();
</script>

<span
	class="inline-flex max-w-full items-center gap-1.5 {compact
		? 'text-[0.75rem] text-faint'
		: 'rounded-full border border-line bg-surface px-2.5 py-1 text-xs font-medium text-muted'} {className}"
>
	<span class="size-1.5 shrink-0 rounded-full" style="background: {STATUS_COLOR[status]}"></span>
	<span class="truncate">
		{#if typeof children === 'function'}{@render children()}{:else if children}{children}{:else}{status}{/if}
	</span>
</span>
