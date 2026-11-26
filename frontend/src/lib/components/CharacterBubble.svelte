<script lang="ts">
	/** Small round picture of a character (their initial when there is no picture). */
	let { src, name, class: className = 'size-5' }: { src?: string | null; name: string; class?: string } = $props();

	let failed = $state(false);
	const usable = $derived(!!src && !failed);
</script>

<span class="inline-grid shrink-0 place-items-center overflow-hidden rounded-full bg-raised align-middle text-[0.625rem] font-semibold text-faint ring-1 ring-line {className}">
	{#if usable}
		<img {src} alt="" loading="lazy" decoding="async" class="size-full object-cover" onerror={() => (failed = true)} />
	{:else}
		{name.trim().charAt(0).toUpperCase()}
	{/if}
</span>
