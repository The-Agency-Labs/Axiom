<script lang="ts">
	import { ChevronRight } from '@lucide/svelte';
	import type { Component } from 'svelte';

	/** Section heading with an icon chip, a count and a 44px "View all" link. */
	let {
		title,
		icon: Icon,
		accent,
		count,
		href,
		linkLabel = 'View all'
	}: {
		title: string;
		icon?: Component<any>;
		accent?: string;
		count?: number;
		href?: string;
		linkLabel?: string;
	} = $props();
</script>

<div class="mb-1 flex min-h-11 items-center justify-between gap-3">
	<div class="flex min-w-0 items-center gap-2.5">
		{#if Icon}
			<span
				class="grid size-7 shrink-0 place-items-center rounded-lg"
				style="background: color-mix(in oklab, {accent ?? 'var(--color-brand)'} 16%, transparent); color: {accent ?? 'var(--color-brand)'}"
			>
				<Icon class="size-4" />
			</span>
		{/if}
		<h2 class="truncate text-[15px] font-semibold tracking-tight">{title}</h2>
		{#if count !== undefined}
			<span class="rounded-full bg-ink/6 px-2 py-0.5 text-xs text-muted tabular-nums">{count}</span>
		{/if}
	</div>
	{#if href}
		<a {href} class="-mr-2 flex min-h-11 items-center gap-0.5 rounded-lg px-2 text-[13px] font-medium text-muted transition hover:text-ink">
			{linkLabel} <ChevronRight class="size-4" />
		</a>
	{/if}
</div>
