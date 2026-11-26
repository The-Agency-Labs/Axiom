<script lang="ts">
	import { ArrowLeft } from '@lucide/svelte';
	import { page } from '$app/state';
	import { get } from '#lib/api.ts';
	import { trackBook } from '#lib/components/reading/series.ts';
	import SeriesBookRow from '#lib/components/reading/SeriesBookRow.svelte';
	import type { SeriesBook, SeriesDetail } from '#lib/components/reading/types.ts';
	import type { Status } from '#lib/types.ts';
	import { EmptyState, IconButton, ProgressBar, Skeleton } from '#lib/ui/index.ts';

	let series = $state<SeriesDetail | null>(null);
	let error = $state('');

	$effect(() => {
		const id = page.params.id;
		series = null;
		error = '';
		get<SeriesDetail>(`/series/${id}`, (d) => (series = d))
			.then((d) => id === page.params.id && (series = d))
			.catch(() => (error = 'This series could not be found.'));
	});

	const done = $derived(series?.books.filter((b) => b.entry?.status === 'Completed').length ?? 0);
	const next = $derived(series?.books.find((b) => b.entry?.status !== 'Completed') ?? null);

	// Rows in order, with a placeholder wherever whole-number positions are missing.
	const rows = $derived.by(() => {
		const out: ({ book: SeriesBook } | { gap: number; from: number })[] = [];
		let prev: number | null = null;
		for (const book of series?.books ?? []) {
			const base = Math.floor(book.position);
			if (prev !== null && base - prev > 1) out.push({ gap: base - prev - 1, from: prev + 1 });
			out.push({ book });
			prev = Math.max(prev ?? base, base);
		}
		return out;
	});
	// The provider lists more books than we have positions for.
	const unlisted = $derived(series ? Math.max(0, series.books_count - series.books.length) : 0);

	function track(book: SeriesBook, status: Status) {
		trackBook(book, status, (entry) => (book.entry = entry));
	}
</script>

<svelte:head><title>{series?.name ?? 'Series'} · Encompass</title></svelte:head>

<div class="mb-4 flex items-center gap-1">
	<IconButton icon={ArrowLeft} label="Back to series" href="/series" />
	<h1 class="min-w-0 flex-1 truncate text-2xl font-semibold tracking-tight">{series?.name ?? ''}</h1>
</div>

{#if error}
	<EmptyState title="Series not found" description={error} />
{:else if !series}
	<div class="space-y-3" aria-hidden="true"><Skeleton class="h-20 w-full" /><Skeleton class="h-64 w-full" /></div>
{:else}
	<div class="mb-4 rounded-[var(--radius-card)] border border-line bg-surface p-4">
		<p class="text-[0.8125rem] text-muted tabular-nums">{done} of {series.books_count} read</p>
		<ProgressBar percent={series.books_count ? (done / series.books_count) * 100 : 0} accent="var(--color-type-book)" label="Series progress" class="mt-2" />
	</div>

	{#if next}
		<section class="mb-5" aria-label="Next up">
			<h2 class="mb-2 px-1 text-[0.8125rem] font-semibold tracking-wide text-faint uppercase">Next up</h2>
			<ul class="overflow-hidden rounded-[var(--radius-card)] border border-brand/40 bg-surface">
				<SeriesBookRow book={next} highlight ontrack={(s) => track(next, s)} />
			</ul>
		</section>
	{/if}

	<h2 class="mb-2 px-1 text-[0.8125rem] font-semibold tracking-wide text-faint uppercase">All books</h2>
	<ul class="divide-y divide-line overflow-hidden rounded-[var(--radius-card)] border border-line bg-surface">
		{#each rows as row, i (i)}
			{#if 'book' in row}
				<SeriesBookRow book={row.book} highlight={row.book === next} ontrack={(s) => track(row.book, s)} />
			{:else}
				<li class="px-4 py-3 text-[0.8125rem] text-faint">
					{row.gap === 1 ? `Book ${row.from} is not listed` : `Books ${row.from}–${row.from + row.gap - 1} are not listed`}
				</li>
			{/if}
		{/each}
		{#if unlisted}
			<li class="px-4 py-3 text-[0.8125rem] text-faint">{unlisted} more {unlisted === 1 ? 'book is' : 'books are'} not listed yet</li>
		{/if}
	</ul>
{/if}
