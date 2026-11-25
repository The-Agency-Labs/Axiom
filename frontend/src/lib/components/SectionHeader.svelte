<script lang="ts">
	import { ChevronRight } from '@lucide/svelte';
	import type { Component } from 'svelte';

	let {
		title,
		icon: Icon,
		accent,
		count,
		href
	}: {
		title: string;
		icon?: Component<any>;
		accent?: string;
		count?: number;
		href?: string;
	} = $props();
</script>

<div class="mb-3 flex items-center justify-between gap-3">
	<div class="flex items-center gap-2.5">
		{#if Icon}
			<span
				class="grid size-7 place-items-center rounded-lg"
				style="background: color-mix(in oklab, {accent} 16%, transparent); color: {accent}"
			>
				<Icon class="size-4" />
			</span>
		{/if}
		<h2 class="text-[15px] font-semibold tracking-tight">{title}</h2>
		{#if count !== undefined}
			<span class="rounded-full bg-ink/6 px-2 py-0.5 text-xs text-muted tabular-nums">{count}</span>
		{/if}
	</div>
	{#if href}
		<a
			{href}
			class="flex items-center gap-0.5 text-xs font-medium text-muted transition hover:text-ink"
		>
			View all <ChevronRight class="size-3.5" />
		</a>
	{/if}
</div>
