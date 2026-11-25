<script lang="ts">
	import type { Component, Snippet } from 'svelte';

	/** Centered empty/error state. Put call-to-action buttons in the `actions` snippet. */
	let {
		icon: Icon,
		title,
		description,
		actions,
		class: className = ''
	}: {
		icon?: Component<any>;
		title: string;
		description?: string;
		actions?: Snippet;
		class?: string;
	} = $props();
</script>

<div class="flex flex-col items-center px-6 py-14 text-center {className}">
	{#if Icon}
		<span class="mb-4 grid size-14 place-items-center rounded-2xl bg-raised text-muted ring-1 ring-line">
			<Icon class="size-6" />
		</span>
	{/if}
	<h2 class="text-base font-semibold">{title}</h2>
	{#if description}<p class="mt-1 max-w-sm text-sm text-muted">{description}</p>{/if}
	{#if actions}<div class="mt-5 flex flex-wrap justify-center gap-2">{@render actions()}</div>{/if}
</div>
