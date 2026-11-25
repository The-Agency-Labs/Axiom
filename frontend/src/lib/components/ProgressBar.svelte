<script lang="ts">
	let {
		percent,
		accent = 'var(--color-brand)',
		class: className = ''
	}: { percent: number | null; accent?: string; class?: string } = $props();

	const width = $derived(Math.max(0, Math.min(100, percent ?? 0)));
</script>

<div class="h-1.5 w-full overflow-hidden rounded-full bg-white/8 {className}">
	<div
		class="h-full rounded-full transition-[width] duration-500 ease-out"
		style="width: {width}%; background: {accent}"
	></div>
</div>
