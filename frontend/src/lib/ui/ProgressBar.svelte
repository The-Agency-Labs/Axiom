<script lang="ts">
	/** Thin progress bar. `percent` null renders an empty track. */
	let {
		percent,
		accent = 'var(--color-brand)',
		label = 'Progress',
		class: className = ''
	}: { percent: number | null; accent?: string; label?: string; class?: string } = $props();

	const width = $derived(Math.max(0, Math.min(100, percent ?? 0)));
</script>

<div
	role="progressbar"
	aria-label={label}
	aria-valuemin="0"
	aria-valuemax="100"
	aria-valuenow={Math.round(width)}
	class="h-1.5 w-full overflow-hidden rounded-full bg-ink/10 {className}"
>
	<div
		class="h-full rounded-full transition-[width] duration-500 ease-out"
		style="width: {width}%; background: {accent}"
	></div>
</div>
