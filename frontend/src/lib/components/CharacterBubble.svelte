<script lang="ts">
	import { picture } from '#lib/media.ts';

	/** Small round picture of a character; nothing at all when there is no picture. */
	let { src, name, class: className = 'size-5' }: { src?: string | null; name: string; class?: string } = $props();

	let failed = $state(false);
	const usable = $derived(!!picture(src) && !failed);
</script>

{#if usable}
	<span class="inline-block shrink-0 overflow-hidden rounded-full bg-raised align-middle ring-1 ring-line {className}">
		<img {src} alt="" title={name} loading="lazy" decoding="async" class="size-full object-cover" onerror={() => (failed = true)} />
	</span>
{/if}
