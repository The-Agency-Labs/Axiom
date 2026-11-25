<script lang="ts">
	/** Switch (role=switch) with a 44px-tall hit area. Pair it with a visible label via `label`. */
	let {
		checked = $bindable(false),
		label,
		disabled = false,
		onchange
	}: { checked: boolean; label: string; disabled?: boolean; onchange?: (checked: boolean) => void } = $props();
</script>

<button
	type="button"
	role="switch"
	aria-checked={checked}
	aria-label={label}
	{disabled}
	onclick={() => {
		checked = !checked;
		onchange?.(checked);
	}}
	class="group grid h-11 w-14 shrink-0 place-items-center disabled:opacity-50"
>
	<span
		class="relative h-7 w-12 rounded-full border border-line-strong transition-colors {checked ? 'bg-brand' : 'bg-ink/12'}"
	>
		<span
			class="absolute top-0.5 left-0.5 size-[22px] rounded-full bg-white shadow transition-transform {checked
				? 'translate-x-5'
				: ''}"
		></span>
	</span>
</button>
