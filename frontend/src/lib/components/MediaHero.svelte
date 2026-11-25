<script lang="ts">
	import { Star } from '@lucide/svelte';
	import type { Snippet } from 'svelte';
	import Cover from '#lib/components/Cover.svelte';
	import { MEDIA } from '#lib/media.ts';
	import type { MediaType } from '#lib/types.ts';

	/** Cover, title, genres, synopsis and details for any media; `children` sits under the genres. */
	let {
		type,
		title,
		image,
		md,
		children
	}: { type: MediaType; title: string; image: string; md: Record<string, any>; children?: Snippet } = $props();

	const meta = $derived(MEDIA[type] ?? MEDIA.tv);
	const genres = $derived<string[]>((md.genres ?? []).map((g: any) => (typeof g === 'string' ? g : g.name)));
	const details = $derived(
		Object.entries(md.details ?? {}).filter(
			([, v]) => v !== null && v !== '' && (typeof v === 'string' || typeof v === 'number')
		) as [string, string | number][]
	);
</script>

<div class="relative">
	<!-- Soft backdrop from the cover art -->
	<div class="pointer-events-none absolute -inset-x-10 -top-24 h-80 overflow-hidden opacity-30">
		<img src={image} alt="" class="h-full w-full scale-110 object-cover blur-3xl" />
		<div class="absolute inset-0 bg-gradient-to-b from-transparent to-[var(--color-canvas)]"></div>
	</div>

	<div class="relative grid gap-8 md:grid-cols-[13rem_1fr] lg:grid-cols-[15rem_1fr]">
		<Cover src={image} {title} {type} class="mx-auto aspect-[2/3] w-44 shadow-2xl shadow-black/50 md:mx-0 md:w-full" />

		<div class="min-w-0">
			<div class="flex items-center gap-2 text-xs font-medium" style="color: {meta.accent}">
				<meta.icon class="size-3.5" />
				{meta.label}
				{#if md.score}
					<span class="ml-2 flex items-center gap-1 text-muted">
						<Star class="size-3 fill-amber-300 text-amber-300" />{md.score}
					</span>
				{/if}
			</div>
			<h1 class="mt-2 text-3xl font-semibold tracking-tight text-balance sm:text-4xl">{title}</h1>

			{#if genres.length}
				<div class="mt-3 flex flex-wrap gap-1.5">
					{#each genres.slice(0, 6) as genre (genre)}
						<span class="rounded-full border border-line px-2.5 py-0.5 text-xs text-muted">{genre}</span>
					{/each}
				</div>
			{/if}

			{@render children?.()}

			{#if md.synopsis}
				<p class="mt-6 max-w-3xl text-[15px] leading-relaxed text-ink/80">{md.synopsis}</p>
			{/if}

			{#if details.length}
				<dl class="mt-6 grid grid-cols-2 gap-x-6 gap-y-3 sm:grid-cols-3 lg:grid-cols-4">
					{#each details.slice(0, 8) as [key, value] (key)}
						<div>
							<dt class="text-[11px] tracking-wide text-faint uppercase">{key.replaceAll('_', ' ')}</dt>
							<dd class="mt-0.5 truncate text-sm">{value}</dd>
						</div>
					{/each}
				</dl>
			{/if}
		</div>
	</div>
</div>
