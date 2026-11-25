<script lang="ts">
	import type { Component } from 'svelte';

	/** Square 44px icon-only button. `label` is mandatory (aria-label + tooltip). */
	let {
		icon: Icon,
		label,
		href,
		variant = 'ghost',
		pressed,
		disabled = false,
		class: className = '',
		onclick,
		...rest
	}: {
		icon: Component<any>;
		label: string;
		href?: string;
		variant?: 'ghost' | 'surface' | 'brand';
		pressed?: boolean;
		disabled?: boolean;
		class?: string;
		onclick?: (e: MouseEvent) => void;
		[key: string]: unknown;
	} = $props();

	const styles = {
		ghost: 'text-muted hover:bg-ink/8 hover:text-ink active:bg-ink/12',
		surface: 'border border-line-strong bg-surface text-ink hover:bg-raised',
		brand: 'bg-brand text-on-brand hover:brightness-110'
	};
	const cls = $derived(
		`inline-grid size-11 shrink-0 place-items-center rounded-full transition active:scale-95 disabled:opacity-40 ${styles[variant]} ${className}`
	);
</script>

{#if href}
	<a {href} class={cls} aria-label={label} title={label} {...rest}><Icon class="size-5" /></a>
{:else}
	<button
		type="button"
		class={cls}
		aria-label={label}
		title={label}
		aria-pressed={pressed}
		{disabled}
		{onclick}
		{...rest}
	>
		<Icon class="size-5" />
	</button>
{/if}
