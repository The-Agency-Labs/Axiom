<script lang="ts">
	import { Minus, Plus } from '@lucide/svelte';

	/**
	 * Stepper with 44px -/+ targets. It does not own the value: it reports `onstep(+1 | -1)` and shows
	 * `display`. Wire onstep to mutate() so the tap works offline and updates optimistically.
	 */
	let {
		display,
		label,
		onstep,
		canDecrement = true,
		canIncrement = true,
		disabled = false,
		class: className = ''
	}: {
		/** Text between the buttons, e.g. "142 / 320 p". */
		display: string;
		/** Accessible name of what is being stepped, e.g. "Pages read". */
		label: string;
		onstep: (direction: 1 | -1) => void;
		canDecrement?: boolean;
		canIncrement?: boolean;
		disabled?: boolean;
		class?: string;
	} = $props();
</script>

<div
	role="group"
	aria-label={label}
	class="inline-flex items-center rounded-full border border-line-strong bg-surface {className}"
>
	<button
		type="button"
		class="grid size-11 place-items-center rounded-full text-ink transition hover:bg-ink/8 active:scale-90 disabled:opacity-30"
		aria-label="Decrease {label}"
		disabled={disabled || !canDecrement}
		onclick={() => onstep(-1)}><Minus class="size-5" /></button
	>
	<output class="min-w-16 px-1 text-center text-sm font-semibold tabular-nums" aria-live="polite">{display}</output>
	<button
		type="button"
		class="grid size-11 place-items-center rounded-full bg-brand text-on-brand transition hover:brightness-110 active:scale-90 disabled:opacity-30"
		aria-label="Increase {label}"
		disabled={disabled || !canIncrement}
		onclick={() => onstep(1)}><Plus class="size-5" /></button
	>
</div>
