<script lang="ts">
	import type { Component, Snippet } from 'svelte';

	/** Surface card with an optional heading row (icon + title + right-side `action`). Content has no padding by default when `flush`. */
	let {
		title,
		description,
		icon: Icon,
		accent,
		flush = false,
		action,
		children,
		class: className = '',
		id
	}: {
		id?: string;
		title?: string;
		description?: string;
		icon?: Component<any>;
		accent?: string;
		flush?: boolean;
		action?: Snippet;
		children?: Snippet;
		class?: string;
	} = $props();
</script>

<section {id} class="scroll-mt-4 rounded-[var(--radius-card)] border border-line bg-surface {className}">
	{#if title}
		<header class="flex items-center gap-3 px-4 pt-4 {flush ? 'pb-2' : 'pb-1'}">
			{#if Icon}
				<span
					class="grid size-9 shrink-0 place-items-center rounded-xl"
					style="background: color-mix(in oklab, {accent ?? 'var(--color-brand)'} 16%, transparent); color: {accent ?? 'var(--color-brand)'}"
				>
					<Icon class="size-[18px]" />
				</span>
			{/if}
			<div class="min-w-0 flex-1">
				<h2 class="truncate text-[15px] font-semibold">{title}</h2>
				{#if description}<p class="text-[13px] text-muted">{description}</p>{/if}
			</div>
			{#if action}{@render action()}{/if}
		</header>
	{/if}
	{#if children}
		<div class={flush ? '' : 'p-4 ' + (title ? 'pt-3' : '')}>{@render children()}</div>
	{/if}
</section>
