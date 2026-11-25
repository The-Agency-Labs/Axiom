<script lang="ts">
	export interface TabOption {
		value: string;
		label: string;
		count?: number;
		disabled?: boolean;
	}

	/**
	 * Tabs / segmented control. variant "segmented" is a pill switch, "underline" a classic tab strip.
	 * Arrow keys move between tabs. Bind `value`; `onchange` fires on user changes.
	 */
	let {
		options,
		value = $bindable(),
		variant = 'segmented',
		label = 'Tabs',
		full = false,
		onchange,
		class: className = ''
	}: {
		options: TabOption[];
		value: string;
		variant?: 'segmented' | 'underline';
		label?: string;
		full?: boolean;
		onchange?: (value: string) => void;
		class?: string;
	} = $props();

	let list: HTMLDivElement;

	function select(v: string) {
		if (v === value) return;
		value = v;
		onchange?.(v);
	}

	function onkeydown(e: KeyboardEvent) {
		const enabled = options.filter((o) => !o.disabled);
		const i = enabled.findIndex((o) => o.value === value);
		let next = -1;
		if (e.key === 'ArrowRight') next = (i + 1) % enabled.length;
		else if (e.key === 'ArrowLeft') next = (i - 1 + enabled.length) % enabled.length;
		else if (e.key === 'Home') next = 0;
		else if (e.key === 'End') next = enabled.length - 1;
		if (next < 0) return;
		e.preventDefault();
		select(enabled[next].value);
		list.querySelector<HTMLElement>(`[data-value="${enabled[next].value}"]`)?.focus();
	}
</script>

<div
	bind:this={list}
	role="tablist"
	aria-label={label}
	{onkeydown}
	tabindex="-1"
	class="flex {variant === 'segmented'
		? 'gap-1 rounded-xl border border-line bg-surface p-1'
		: 'gap-1 border-b border-line'} {full ? 'w-full' : 'w-fit max-w-full overflow-x-auto'} {className}"
>
	{#each options as option (option.value)}
		{@const active = option.value === value}
		<button
			type="button"
			role="tab"
			data-value={option.value}
			aria-selected={active}
			tabindex={active ? 0 : -1}
			disabled={option.disabled}
			onclick={() => select(option.value)}
			class="inline-flex min-h-11 items-center justify-center gap-1.5 px-3.5 text-sm font-medium whitespace-nowrap transition disabled:opacity-40 {full
				? 'flex-1'
				: ''} {variant === 'segmented'
				? `rounded-lg ${active ? 'bg-ink/12 text-ink' : 'text-muted hover:text-ink'}`
				: `-mb-px border-b-2 ${active ? 'border-brand text-ink' : 'border-transparent text-muted hover:text-ink'}`}"
		>
			{option.label}
			{#if option.count !== undefined}<span class="text-xs text-faint tabular-nums">{option.count}</span>{/if}
		</button>
	{/each}
</div>
