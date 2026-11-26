<script lang="ts">
	import { Library, Search } from '@lucide/svelte';
	import { get, qs } from '#lib/api.ts';
	import Cover from '#lib/components/Cover.svelte';
	import { SOURCE_NAMES } from '#lib/media.ts';
	import { trackBook } from '#lib/components/reading/series.ts';
	import type { SeriesHit, SeriesSummary } from '#lib/components/reading/types.ts';
	import { Button, EmptyState, ProgressBar, PullToRefresh, Skeleton } from '#lib/ui/index.ts';

	let list = $state<SeriesSummary[] | null>(null);
	let query = $state('');
	let hits = $state<SeriesHit[] | null>(null);
	let searching = $state(false);

	async function load() {
		try {
			const d = await get<{ results: SeriesSummary[] }>('/series', (f) => (list = f.results));
			list = d.results;
		} catch {
			list ??= [];
		}
	}
	$effect(() => {
		load();
	});

	// Search by series name: local series first, then Hardcover. Debounced.
	$effect(() => {
		const q = query.trim();
		if (q.length < 2) {
			hits = null;
			searching = false;
			return;
		}
		searching = true;
		const t = setTimeout(async () => {
			try {
				const d = await get<{ results: SeriesHit[] }>('/series/search' + qs({ q }));
				if (q === query.trim()) hits = d.results;
			} catch {
				hits = [];
			} finally {
				if (q === query.trim()) searching = false;
			}
		}, 350);
		return () => clearTimeout(t);
	});

	const hitHref = (h: SeriesHit) => (h.id != null ? `/series/${h.id}` : `/series/${h.source}/${h.external_id}`);

	function start(s: SeriesSummary, status: 'Planning' | 'In progress') {
		const n = s.next;
		if (!n) return;
		const wasTracked = n.status !== null;
		let counted = false;
		const book = {
			title: n.title,
			source: n.source,
			media_id: n.media_id,
			entry: n.status ? { id: 0, status: n.status, progress: 0, score: null } : null
		};
		return trackBook(book, status, (entry) => {
			n.status = entry?.status ?? null;
			if (entry && !wasTracked && !counted) (s.tracked += 1), (counted = true);
			if (!entry && counted) (s.tracked -= 1), (counted = false);
		});
	}
</script>

<svelte:head><title>Series · Axiom</title></svelte:head>

<PullToRefresh onrefresh={load}>
	<header class="mb-4">
		<h1 class="text-3xl font-semibold tracking-tight">Series</h1>
		<p class="mt-1 text-sm text-muted">Book series you are reading, and where to pick up next.</p>
	</header>

	<div class="relative mb-5">
		<Search class="pointer-events-none absolute top-1/2 left-3.5 size-[18px] -translate-y-1/2 text-faint" />
		<input
			type="search"
			class="input pl-10"
			placeholder="Search for a series"
			aria-label="Search for a series"
			bind:value={query}
			autocomplete="off"
		/>
	</div>

	{#if hits !== null || searching}
		<section aria-label="Series search results" class="mb-8">
			<h2 class="mb-2 px-1 text-[0.8125rem] font-semibold tracking-wide text-faint uppercase">Search results</h2>
			{#if searching && !hits}
				<div class="space-y-2" aria-hidden="true"><Skeleton class="h-16 w-full" /><Skeleton class="h-16 w-full" /></div>
			{:else if hits && hits.length === 0}
				<p class="rounded-2xl border border-dashed border-line-strong p-5 text-sm text-muted">No series match "{query}".</p>
			{:else if hits}
				<ul class="divide-y divide-line overflow-hidden rounded-[var(--radius-card)] border border-line bg-surface">
					{#each hits as h (h.source + h.external_id)}
						<li>
							<a href={hitHref(h)} class="flex min-h-14 items-center gap-3 px-4 py-2.5 hover:bg-ink/5 active:bg-ink/8">
								<span class="min-w-0 flex-1">
									<span class="block truncate text-[0.9375rem] font-medium">{h.name}</span>
									<span class="block text-[0.8125rem] text-muted">
										{h.books_count} {h.books_count === 1 ? 'book' : 'books'}{h.tracked ? ` · ${h.tracked} tracked` : ''}{h.id == null ? ` · ${SOURCE_NAMES[h.source] ?? h.source}` : ''}
									</span>
								</span>
							</a>
						</li>
					{/each}
				</ul>
			{/if}
		</section>
	{/if}

	{#if list === null}
		<div class="space-y-3" aria-hidden="true"><Skeleton class="h-36 w-full" /><Skeleton class="h-36 w-full" /></div>
	{:else if list.length === 0}
		<EmptyState
			icon={Library}
			title="No series yet"
			description="Track a book that belongs to a series and it shows up here. You can also search for a series above."
		/>
	{:else}
		<ul class="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3">
			{#each list as s (s.id)}
				<li class="rounded-[var(--radius-card)] border border-line bg-surface p-3">
					<a href="/series/{s.id}" class="flex items-center gap-3">
						<Cover src={s.image} title={s.name} type="book" class="aspect-[2/3] w-14 shrink-0" />
						<span class="min-w-0 flex-1">
							<span class="line-clamp-2 text-[1rem] leading-snug font-semibold">{s.name}</span>
							<span class="mt-1 block text-[0.8125rem] text-muted tabular-nums">
								{s.completed} of {s.total} read{s.tracked > s.completed ? ` · ${s.tracked - s.completed} in your library` : ''}
							</span>
							<ProgressBar percent={s.total ? (s.completed / s.total) * 100 : 0} accent="var(--color-type-book)" label="{s.name} read" class="mt-2" />
						</span>
					</a>
					{#if s.next}
						<div class="mt-3 flex items-center gap-3 border-t border-line pt-3">
							<div class="min-w-0 flex-1">
								<p class="text-[0.75rem] font-semibold tracking-wide text-faint uppercase">Next up</p>
								<p class="truncate text-[0.875rem] font-medium">{s.next.title}</p>
								<p class="text-[0.75rem] text-faint">Book {s.next.position}{s.next.status ? ` · ${s.next.status}` : ''}</p>
							</div>
							{#if s.next.status === null}
								<Button size="sm" variant="secondary" onclick={() => start(s, 'Planning')}>Track</Button>
								<Button size="sm" variant="primary" onclick={() => start(s, 'In progress')}>Start</Button>
							{:else if s.next.status === 'Planning' || s.next.status === 'Paused'}
								<Button size="sm" variant="primary" onclick={() => start(s, 'In progress')}>Start</Button>
							{:else}
								<Button size="sm" variant="secondary" href="/media/book/{s.next.source}/{s.next.media_id}">Open</Button>
							{/if}
						</div>
					{:else}
						<p class="mt-3 border-t border-line pt-3 text-[0.8125rem] text-muted">All caught up.</p>
					{/if}
				</li>
			{/each}
		</ul>
	{/if}
</PullToRefresh>
