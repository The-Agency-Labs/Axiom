<script lang="ts">
	import { LoaderCircle } from '@lucide/svelte';
	import type { Component, Snippet } from 'svelte';

	/**
	 * Button (or link when `href` is set). Always >= 44px tall.
	 * variant: primary (brand fill) | secondary (outlined surface) | ghost | danger
	 */
	let {
		variant = 'secondary',
		size = 'md',
		href,
		type = 'button',
		loading = false,
		disabled = false,
		icon: Icon,
		full = false,
		class: className = '',
		onclick,
		children,
		...rest
	}: {
		variant?: 'primary' | 'secondary' | 'ghost' | 'danger';
		size?: 'md' | 'sm';
		href?: string;
		type?: 'button' | 'submit' | 'reset';
		loading?: boolean;
		disabled?: boolean;
		icon?: Component<any>;
		full?: boolean;
		class?: string;
		onclick?: (e: MouseEvent) => void;
		children?: Snippet;
		[key: string]: unknown;
	} = $props();

	const styles = {
		primary: 'bg-brand text-on-brand hover:brightness-110 active:brightness-95',
		secondary: 'border border-line-strong bg-surface text-ink hover:bg-raised active:bg-hover',
		ghost: 'text-ink hover:bg-ink/6 active:bg-ink/10',
		danger: 'border border-danger/40 bg-danger/10 text-danger hover:bg-danger/16 active:bg-danger/20'
	};
	const sizes = { md: 'px-4 text-[15px]', sm: 'px-3 text-sm' };
	const cls = $derived(
		`inline-flex min-h-11 select-none items-center justify-center gap-2 rounded-xl font-medium whitespace-nowrap transition active:scale-[0.98] disabled:pointer-events-none disabled:opacity-50 aria-disabled:pointer-events-none aria-disabled:opacity-50 ${styles[variant]} ${sizes[size]} ${full ? 'w-full' : ''} ${className}`
	);
</script>

{#if href && !disabled}
	<a {href} class={cls} {...rest}>
		{#if Icon}<Icon class="size-[18px]" />{/if}
		{@render children?.()}
	</a>
{:else}
	<button {type} class={cls} disabled={disabled || loading} aria-busy={loading || undefined} {onclick} {...rest}>
		{#if loading}<LoaderCircle class="size-[18px] animate-spin" />{:else if Icon}<Icon class="size-[18px]" />{/if}
		{@render children?.()}
	</button>
{/if}
