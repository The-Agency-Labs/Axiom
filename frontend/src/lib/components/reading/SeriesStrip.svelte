<script lang="ts">
	import { ChevronRight, Library } from '@lucide/svelte';
	import { get } from '#lib/api.ts';
	import Cover from '#lib/components/Cover.svelte';
	import { Card, Skeleton, StatusBadge } from '#lib/ui/index.ts';
	import { bookHref } from './format.ts';
	import type { SeriesDetail } from './types.ts';

	/** Horizontal strip of a series' books in order; `currentId` is the item shown on the page. */
	let { seriesId, currentId, currentMediaId }: { seriesId: number; currentId?: number | null; currentMediaId?: string | null } = $props();

	let series = $state<SeriesDetail | null>(null);
	$effect(() => {
		const id = seriesId;
		series = null;
		get<SeriesDetail>(`/series/${id}`)
			.then((s) => id === seriesId && (series = s))
			.catch(() => {});
	});

	const isCurrent = (b: SeriesDetail['books'][number]) =>
		(currentId != null && b.item_id === currentId) || (!!currentMediaId && b.media_id === currentMediaId);

	// Gaps: whole-number positions missing between the first and last known book.
	const gapsBefore = $derived.by(() => {
		const out = new Map<number, number>();
		const books = series?.books ?? [];
		for (let i = 1; i < books.length; i++) {
			const gap = Math.floor(books[i].position) - Math.floor(books[i - 1].position) - 1;
			if (gap > 0) out.set(i, gap);
		}
		return out;
	});
</script>

{#if series && series.books.length > 1}
	<Card
		title={series.name}
		description="{series.books.length} of {series.books_count} books"
		icon={Library}
		accent="var(--color-type-book)"
	>
		{#snippet action()}
			<a href="/series/{series!.id}" class="inline-flex min-h-11 items-center gap-0.5 text-[13px] font-medium text-brand">
				Series<ChevronRight class="size-4" />
			</a>
		{/snippet}
		<ol class="shelf -mx-4 flex gap-3 overflow-x-auto px-4 pb-1" aria-label="Books in {series.name}">
			{#each series.books as b, i (b.source + b.media_id)}
				{@const current = isCurrent(b)}
				{#if gapsBefore.get(i)}
					<li class="grid w-14 shrink-0 place-items-center self-start" style="aspect-ratio: 2/3" aria-label="{gapsBefore.get(i)} missing">
						<span class="rounded-full border border-dashed border-line-strong px-2 py-0.5 text-[11px] text-faint">+{gapsBefore.get(i)}</span>
					</li>
				{/if}
				<li class="w-24 shrink-0">
					<a
						href={bookHref(b)}
						aria-current={current ? 'page' : undefined}
						aria-label="{b.position}. {b.title}"
						class="block rounded-xl p-1 {current ? 'bg-ink/8 ring-2 ring-brand' : ''}"
					>
						<Cover src={b.image} title={b.title} type="book" class="aspect-[2/3] w-full" />
						<p class="mt-1.5 text-[11px] font-semibold text-faint tabular-nums">Book {b.position}</p>
						<p class="line-clamp-2 text-[12px] leading-snug">{b.title}</p>
						{#if b.entry}<StatusBadge status={b.entry.status} compact class="mt-0.5" />{:else}<span class="text-[11px] text-faint">Not tracked</span>{/if}
					</a>
				</li>
			{/each}
		</ol>
	</Card>
{/if}
