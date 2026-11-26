<script lang="ts">
	import { ArrowLeft, Star } from '@lucide/svelte';
	import type { Snippet } from 'svelte';
	import Cover from '#lib/components/Cover.svelte';
	import { MEDIA, SOURCE_NAMES } from '#lib/media.ts';
	import type { MediaType } from '#lib/types.ts';
	import { IconButton, ReleaseBadge, Skeleton } from '#lib/ui/index.ts';

	/** Backdrop, poster, title, native title, year, type badge, genres and the provider rating. */
	let {
		kind,
		title,
		nativeTitle,
		image,
		backdrop,
		year,
		genres = [],
		rating,
		source,
		loadingMeta = false,
		release,
		back,
		children
	}: {
		kind: MediaType;
		title: string;
		nativeTitle?: string;
		image?: string | null;
		/** Wide background art from the provider (TMDB, TVDB, IGDB), when it has one. */
		backdrop?: string | null;
		year?: string | null;
		genres?: string[];
		rating?: number | null;
		source?: string;
		loadingMeta?: boolean;
		/** Release status of the title itself (catalog.ReleaseStatus). */
		release?: string | null;
		back: () => void;
		children?: Snippet;
	} = $props();

	const meta = $derived(MEDIA[kind] ?? MEDIA.tv);
	const showNative = $derived(!!nativeTitle && nativeTitle !== title);
</script>

<header class="relative -mx-4 -mt-5 overflow-hidden px-4 pt-5 pb-4 sm:-mx-6 sm:px-6 lg:-mx-10 lg:-mt-10 lg:px-10 lg:pt-10">
	{#if backdrop}
		<!-- The provider's banner, sharp, fading into the page under the title. -->
		<div class="pointer-events-none absolute inset-x-0 top-0 aspect-video max-h-[28rem] w-full" aria-hidden="true">
			<img src={backdrop} alt="" class="h-full w-full object-cover object-top" />
			<div class="absolute inset-0 bg-gradient-to-b from-canvas/30 via-canvas/10 to-canvas"></div>
		</div>
	{:else if image && !image.includes('none.svg')}
		<div class="pointer-events-none absolute inset-0 opacity-35" aria-hidden="true">
			<img src={image} alt="" class="h-full w-full scale-125 object-cover blur-2xl" />
		</div>
		<div class="pointer-events-none absolute inset-0 bg-gradient-to-b from-transparent via-canvas/60 to-canvas" aria-hidden="true"></div>
	{/if}

	<div class="relative">
		<IconButton icon={ArrowLeft} label="Back" variant="surface" class="-ml-1 bg-canvas/60 backdrop-blur" onclick={back} />

		<div class="{backdrop ? 'mt-[22vw] sm:mt-40 lg:mt-56' : 'mt-3'} grid grid-cols-[7.5rem_1fr] gap-4 sm:grid-cols-[9rem_1fr] lg:grid-cols-[12rem_1fr] lg:gap-8">
			<Cover
				src={image}
				{title}
				type={kind}
				class="aspect-[2/3] w-full shadow-2xl shadow-black/40"
			/>
			<div class="min-w-0 self-end lg:self-center">
				<p class="flex flex-wrap items-center gap-x-2 text-xs font-semibold" style="color: {meta.accent}">
					<span class="inline-flex items-center gap-1.5"><meta.icon class="size-3.5" />{meta.label}</span>
					{#if source && source !== 'tmdb'}<span class="font-medium text-faint">{SOURCE_NAMES[source] ?? source}</span>{/if}
				</p>
				<h1 class="mt-1 text-2xl font-semibold tracking-tight text-balance sm:text-3xl lg:text-4xl">{title}</h1>
				{#if showNative}<p class="mt-0.5 text-[0.9375rem] text-muted">{nativeTitle}</p>{/if}
				<p class="mt-2 flex min-h-5 flex-wrap items-center gap-x-3 text-sm text-muted">
					{#if year}<span class="tabular-nums">{year}</span>{/if}
					<ReleaseBadge status={release} />
					{#if rating}
						<span class="inline-flex items-center gap-1 tabular-nums">
							<Star class="size-3.5 fill-amber-300 text-amber-300" />{Number(rating).toFixed(1)}
						</span>
					{/if}
				</p>
			</div>
		</div>

		<div class="mt-3 flex min-h-8 flex-wrap gap-1.5">
			{#if loadingMeta && genres.length === 0}
				<Skeleton class="h-7 w-16 rounded-full" />
				<Skeleton class="h-7 w-20 rounded-full" />
			{:else}
				{#each genres.slice(0, 6) as genre (genre)}
					<span class="rounded-full border border-line bg-surface/70 px-2.5 py-1 text-xs text-muted">{genre}</span>
				{/each}
			{/if}
		</div>

		{@render children?.()}
	</div>
</header>
