<script lang="ts">
	import { BookOpen } from '@lucide/svelte';
	import { untrack } from 'svelte';
	import { get, qs } from '#lib/api.ts';
	import Cover from '#lib/components/Cover.svelte';
	import { minutesLabel } from '#lib/media.ts';
	import { Button, EmptyState, Skeleton } from '#lib/ui/index.ts';
	import { bookTitle, dayHeading, isoDaysAgo } from './format.ts';
	import type { JournalDay } from './types.ts';

	/** Bump `version` to reload from scratch (after linking a book, pull-to-refresh). */
	let { version = 0 }: { version?: number } = $props();

	const CHUNK = 30; // days per request
	let days = $state<JournalDay[]>([]);
	let loaded = $state(false);
	let loading = $state(false);
	let offset = 0; // days already covered, counted back from today
	let emptyRun = 0;
	let exhausted = $state(false);
	let sentinel = $state<HTMLElement>();
	let generation = 0;

	async function more() {
		if (loading || exhausted) return;
		loading = true;
		const gen = generation;
		try {
			const end = isoDaysAgo(offset);
			const start = isoDaysAgo(offset + CHUNK - 1);
			const chunk = await get<JournalDay[]>('/reading/days' + qs({ start, end }));
			if (gen !== generation) return;
			offset += CHUNK;
			days = [...days, ...chunk];
			emptyRun = chunk.length ? 0 : emptyRun + 1;
			// Two empty months in a row (or ~3 years) and we stop auto-loading.
			if (emptyRun >= 2 || offset > 1100) exhausted = true;
		} catch {
			exhausted = true;
		} finally {
			if (gen === generation) {
				loading = false;
				loaded = true;
			}
		}
	}

	$effect(() => {
		void version;
		untrack(() => {
			generation++;
			days = [];
			offset = 0;
			emptyRun = 0;
			exhausted = false;
			loaded = false;
			loading = false;
			more();
		});
	});

	// Infinite scroll: load the next range when the sentinel nears the viewport.
	$effect(() => {
		if (!sentinel || exhausted) return;
		const io = new IntersectionObserver((entries) => entries[0].isIntersecting && more(), { rootMargin: '400px' });
		io.observe(sentinel);
		return () => io.disconnect();
	});

	const pageRange = (b: { from_page: number | null; to_page: number | null }) =>
		b.from_page != null && b.to_page != null && b.to_page > b.from_page ? `p. ${b.from_page}–${b.to_page}` : '';
</script>

{#if !loaded}
	<div class="space-y-3" aria-hidden="true">
		<Skeleton class="h-5 w-40" />
		<Skeleton class="h-20 w-full" />
		<Skeleton class="h-20 w-full" />
	</div>
{:else if days.length === 0}
	<EmptyState icon={BookOpen} title="No reading logged yet" description="Sessions from KOReader and your Kindle show up here, one row per book per day." />
{:else}
	<div class="space-y-6">
		{#each days as day (day.date)}
			<section aria-label={dayHeading(day.date)}>
				<header class="mb-2 flex items-baseline justify-between gap-3 px-1">
					<h3 class="text-[0.9375rem] font-semibold">{dayHeading(day.date)}</h3>
					<!-- Day totals only add something when there is more than one book. -->
					{#if day.books.length > 1}
						<p class="text-[0.8125rem] text-muted tabular-nums">
							{day.pages ? `${day.pages} pages` : ''}{day.pages && day.minutes ? ' · ' : ''}{day.minutes
								? minutesLabel(day.minutes)
								: ''}
						</p>
					{/if}
				</header>
				<ul class="divide-y divide-line overflow-hidden rounded-[var(--radius-card)] border border-line bg-surface">
					{#each day.books as b, i (b.item_id ?? b.document_id ?? i)}
						<li class="flex items-center gap-3 px-3 py-2.5">
							<Cover src={b.image} title={b.title} type="book" class="aspect-[2/3] w-10 shrink-0 !rounded-md" />
							<div class="min-w-0 flex-1">
								<p class="line-clamp-2 text-[0.875rem] leading-snug font-medium">
									{bookTitle(b.title, b.series, b.series_number)}
								</p>
								<p class="mt-0.5 text-[0.75rem] text-faint tabular-nums">
									{#if b.pages}{b.pages} pages{:else if b.chapters}{b.chapters} ch{/if}{#if pageRange(b)}
										· {pageRange(b)}{/if}
								</p>
							</div>
							<span class="shrink-0 text-[0.8125rem] font-medium text-muted tabular-nums">{minutesLabel(b.minutes)}</span>
						</li>
					{/each}
				</ul>
			</section>
		{/each}
	</div>
	<div bind:this={sentinel} class="py-6 text-center">
		{#if loading}
			<Skeleton class="mx-auto h-5 w-32" />
		{:else if exhausted}
			<Button
				variant="ghost"
				onclick={() => {
					exhausted = false;
					emptyRun = 0;
					more();
				}}>Look further back</Button
			>
		{/if}
	</div>
{/if}
