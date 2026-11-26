<script lang="ts">
	import type { Component, Snippet } from 'svelte';

	/**
	 * Filter/status chip. Looks 36px tall but its hit area extends to 44px.
	 * Use inside <ChipRow>. `selected` highlights it; `color` tints the dot/icon.
	 */
	let {
		selected = false,
		href,
		count,
		color,
		icon: Icon,
		class: className = '',
		onclick,
		children,
		...rest
	}: {
		selected?: boolean;
		href?: string;
		count?: number | string;
		color?: string;
		icon?: Component<any>;
		class?: string;
		onclick?: (e: MouseEvent) => void;
		children?: Snippet;
		[key: string]: unknown;
	} = $props();

	const cls = $derived(
		`relative inline-flex h-9 shrink-0 items-center gap-1.5 rounded-full border px-3.5 text-[0.8125rem] font-medium whitespace-nowrap transition before:absolute before:-inset-y-1 before:inset-x-0 before:content-[''] ${
			selected
				? 'border-line-strong bg-ink/10 text-ink'
				: 'border-line bg-surface text-muted hover:text-ink'
		} ${className}`
	);
</script>

{#snippet inner()}
	{#if Icon}<Icon class="size-3.5" style={color ? `color: ${color}` : undefined} />{:else if color}<span
			class="size-1.5 rounded-full"
			style="background: {color}"
		></span>{/if}
	{@render children?.()}
	{#if count !== undefined}<span class="text-faint tabular-nums">{count}</span>{/if}
{/snippet}

{#if href}
	<a {href} class={cls} aria-current={selected ? 'page' : undefined} {...rest}>{@render inner()}</a>
{:else}
	<button type="button" class={cls} aria-pressed={selected} {onclick} {...rest}>{@render inner()}</button>
{/if}
