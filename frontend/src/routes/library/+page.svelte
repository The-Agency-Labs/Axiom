<script lang="ts">
	import { ChevronRight } from '@lucide/svelte';
	import { LIBRARY_TYPES, MEDIA } from '#lib/media.ts';
</script>

<svelte:head><title>Library · Encompass</title></svelte:head>

<h1 class="mb-6 text-3xl font-semibold tracking-tight">Library</h1>
<div class="grid gap-2 sm:grid-cols-2">
	{#each LIBRARY_TYPES as type (type)}
		{@const meta = MEDIA[type]}
		<a
			href="/library/{type}"
			class="flex items-center gap-3 rounded-2xl border border-line bg-surface p-4 transition hover:border-line-strong hover:bg-raised"
		>
			<span
				class="grid size-10 place-items-center rounded-xl"
				style="background: color-mix(in oklab, {meta.accent} 16%, transparent); color: {meta.accent}"
			>
				<meta.icon class="size-5" />
			</span>
			<span class="flex-1 font-medium">{meta.plural}</span>
			<ChevronRight class="size-4 text-faint" />
		</a>
	{/each}
</div>
