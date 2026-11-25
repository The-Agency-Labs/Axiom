<script lang="ts">
	import { ChevronRight } from '@lucide/svelte';
	import type { Snippet } from 'svelte';

	/**
	 * A 56px+ tappable row: leading visual, title/subtitle, trailing content, optional chevron.
	 * Renders a link when `href` is set, a button when only `onclick` is given, else a plain row.
	 */
	let {
		title,
		subtitle,
		href,
		onclick,
		chevron,
		leading,
		trailing,
		class: className = ''
	}: {
		title: string;
		subtitle?: string;
		href?: string;
		onclick?: (e: MouseEvent) => void;
		/** Defaults to true for links. */
		chevron?: boolean;
		leading?: Snippet;
		trailing?: Snippet;
		class?: string;
	} = $props();

	const showChevron = $derived(chevron ?? !!href);
	const cls = $derived(
		`flex min-h-14 w-full items-center gap-3 px-4 py-2.5 text-left transition ${href || onclick ? 'hover:bg-ink/5 active:bg-ink/8' : ''} ${className}`
	);
</script>

{#snippet content()}
	{#if leading}{@render leading()}{/if}
	<span class="min-w-0 flex-1">
		<span class="block truncate text-[15px] font-medium">{title}</span>
		{#if subtitle}<span class="block truncate text-[13px] text-muted">{subtitle}</span>{/if}
	</span>
	{#if trailing}{@render trailing()}{/if}
	{#if showChevron}<ChevronRight class="size-4 shrink-0 text-faint" />{/if}
{/snippet}

{#if href}
	<a {href} class={cls}>{@render content()}</a>
{:else if onclick}
	<button type="button" class={cls} {onclick}>{@render content()}</button>
{:else}
	<div class={cls}>{@render content()}</div>
{/if}
