<script lang="ts">
	import ReleaseBadge from './ReleaseBadge.svelte';
	import { Star } from '@lucide/svelte';
	import Cover from '#lib/components/Cover.svelte';
	import StatusBadge from './StatusBadge.svelte';
	import ProgressBar from './ProgressBar.svelte';
	import { MEDIA } from '#lib/media.ts';
	import type { MediaType, Status } from '#lib/types.ts';
	import type { Snippet } from 'svelte';

	/**
	 * Poster tile: fixed 2:3 cover (no layout shift), lazy image, two-line title, subtitle line.
	 * Optional score pill, status badge and progress bar. Use inside <PosterGrid>.
	 * `overlay` renders in the top-left corner of the cover (e.g. a "+1" quick action).
	 */
	let {
		href,
		src,
		title,
		type,
		subtitle,
		status,
		score,
		progress,
		release,
		overlay
	}: {
		href: string;
		src?: string | null;
		title: string;
		type: MediaType;
		subtitle?: string;
		status?: Status;
		score?: number | null;
		/** 0-100; shows a thin bar under the title. */
		progress?: number | null;
		/** Release status of the title itself (catalog.ReleaseStatus). */
		release?: string | null;
		overlay?: Snippet;
	} = $props();
</script>

<div class="group relative min-w-0">
	<a {href} class="block rounded-xl" aria-label={title}>
		<div class="relative">
			<Cover
				{src}
				{title}
				{type}
				class="aspect-[2/3] w-full transition duration-300 group-hover:-translate-y-0.5 group-hover:ring-line-strong"
			/>
			{#if score}
				<span
					class="absolute top-2 right-2 flex items-center gap-1 rounded-full bg-black/70 px-2 py-0.5 text-[0.75rem] font-semibold text-white backdrop-blur"
				>
					<Star class="size-3 fill-amber-300 text-amber-300" />{score}
				</span>
			{/if}
			{#if release && progress == null}
				<ReleaseBadge status={release} onCover class="absolute bottom-2 left-2" />
			{/if}
			{#if progress != null}
				<div class="absolute inset-x-2 bottom-2">
					<ProgressBar
						percent={progress}
						accent={MEDIA[type].accent}
						label="{title} progress"
						class="h-1 bg-black/50"
					/>
				</div>
			{/if}
		</div>
		<p class="mt-2 line-clamp-2 text-[0.8125rem] leading-snug font-medium" aria-hidden="true">{title}</p>
		{#if status}
			<StatusBadge {status} compact class="mt-0.5">{subtitle}</StatusBadge>
		{:else if subtitle}
			<p class="mt-0.5 truncate text-[0.75rem] text-faint">{subtitle}</p>
		{/if}
	</a>
	{#if overlay}<div class="absolute top-1.5 left-1.5">{@render overlay()}</div>{/if}
</div>
