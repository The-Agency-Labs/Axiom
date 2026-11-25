<script lang="ts">
	import { MEDIA } from '#lib/media.ts';
	import type { MediaType } from '#lib/types.ts';

	let {
		src,
		title,
		type,
		class: className = ''
	}: { src?: string | null; title: string; type: MediaType; class?: string } = $props();

	let failed = $state(false);
	const meta = $derived(MEDIA[type]);
	const usable = $derived(!!src && !failed && !src.includes('none.svg'));
</script>

<div
	class="relative overflow-hidden rounded-xl bg-raised ring-1 ring-line {className}"
	style="--accent: {meta.accent}"
>
	{#if usable}
		<img
			{src}
			alt=""
			loading="lazy"
			decoding="async"
			class="h-full w-full object-cover"
			onerror={() => (failed = true)}
		/>
	{:else}
		<div
			class="flex h-full w-full flex-col items-center justify-center gap-2 p-3 text-center"
			style="background: linear-gradient(160deg, color-mix(in oklab, var(--accent) 22%, transparent), transparent)"
		>
			<meta.icon class="size-6 opacity-60" />
			<span class="line-clamp-3 text-xs font-medium text-muted">{title}</span>
		</div>
	{/if}
</div>
