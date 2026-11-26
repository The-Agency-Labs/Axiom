<script lang="ts">
	import { ArrowDownUp, ChevronDown, LayoutGrid, List, Plus, Search, Star, X } from '@lucide/svelte';
	import { page } from '$app/state';
	import { get, qs } from '#lib/api.ts';
	import Cover from '#lib/components/Cover.svelte';
	import SortSheet from '#lib/components/library/SortSheet.svelte';
	import { hasEpisodes, isMediaType, MEDIA, NO_STATUS, progressLabel, STATUS_COLOR, STATUSES, titleHref, yearView } from '#lib/media.ts';
	import { onSynced } from '#lib/offline.svelte.ts';
	import { settings, typeDefaults } from '#lib/settings.svelte.ts';
	import type { Entry, LibraryResponse, MediaType, SeasonFinished, Status } from '#lib/types.ts';
	import {
		Button,
		Chip,
		ChipRow,
		EmptyState,
		IconButton,
		ListRow,
		PosterCard,
		PosterGrid,
		PullToRefresh,
		Skeleton
	} from '#lib/ui/index.ts';

	const PAGE = 60;
	const CHIPS: (Status | 'All')[] = ['In progress', 'Planning', 'Completed', 'Paused', 'Dropped', 'All'];

	const type = $derived(page.params.type as MediaType);
	const valid = $derived(isMediaType(type));
	const meta = $derived(valid ? MEDIA[type] : MEDIA.tv);
	// Games are just a list: no status, and "the year" is the year added.
	const isGame = $derived(NO_STATUS.has(type)); // games and board games: no status
	const yv = $derived(yearView(type));

	let entries = $state<Entry[] | null>(null);
	let years = $state<number[]>([]);
	let failed = $state(false);
	let status = $state<Status | 'All'>('Completed');
	let sort = $state('year');
	let query = $state('');
	let year = $state<string>('');
	let view = $state<'grid' | 'list'>('grid');
	let sortOpen = $state(false);
	let shown = $state(PAGE);
	// Keep the user's choices once they touch a control; otherwise follow the per-type defaults.
	let touched = $state(false);
	// Shows by year are by season: each season under the year it was finished, with its own art.
	const hasSeasons = $derived((type === 'tv' || type === 'anime') && sort === 'year');
	let bySeason = $state(false);
	const seasonView = $derived(hasSeasons && bySeason);
	$effect(() => {
		try {
			bySeason = localStorage.getItem(`encompass-by-season-${type}`) === '1';
		} catch {
			bySeason = false;
		}
	});
	function setBySeason(v: boolean) {
		bySeason = v;
		try {
			localStorage.setItem(`encompass-by-season-${type}`, v ? '1' : '0');
		} catch {
			/* storage unavailable */
		}
	}
	let seasons = $state<SeasonFinished[] | null>(null);
	// Year headings fold: the newest year starts open, the rest closed; choices are kept per type.
	let folds = $state<Record<string, boolean>>({});
	const foldKey = () => `encompass-year-folds-${type}`;
	$effect(() => {
		try {
			folds = JSON.parse(localStorage.getItem(foldKey()) ?? '{}');
		} catch {
			folds = {};
		}
	});
	const isOpen = (label: string, i: number) => folds[label] ?? i === 0;
	function toggleYear(label: string, i: number) {
		folds = { ...folds, [label]: !isOpen(label, i) };
		try {
			localStorage.setItem(foldKey(), JSON.stringify(folds));
		} catch {
			/* storage unavailable */
		}
	}

	try {
		const saved = localStorage.getItem('encompass-library-view');
		if (saved === 'list' || saved === 'grid') view = saved;
	} catch {
		/* storage unavailable */
	}
	function setView(v: 'grid' | 'list') {
		view = v;
		try {
			localStorage.setItem('encompass-library-view', v);
		} catch {
			/* storage unavailable */
		}
	}

	export const snapshot = {
		capture: () => ({ type, status, sort, query, year, touched }),
		restore: (v: { type: string; status: Status | 'All'; sort: string; query: string; year: string; touched: boolean }) => {
			if (v.type !== type) return;
			({ status, sort, query, year, touched } = v);
		}
	};

	// New type: back to defaults.
	let lastType = '';
	$effect(() => {
		if (type === lastType) return;
		lastType = type;
		entries = null;
		touched = false;
		query = '';
		year = '';
		shown = PAGE;
	});

	// Defaults from settings (they load asynchronously after sign-in).
	$effect(() => {
		void settings.loaded;
		if (touched || !valid) return;
		const d = typeDefaults(type);
		sort = d?.sort ?? 'year';
		status = (d?.status as Status | 'All') ?? 'Completed';
	});

	async function load(initial = false) {
		const kind = type;
		const y = year;
		failed = false;
		try {
			const data = await get<LibraryResponse>(
				'/library' + qs({ media_type: kind, year: y, limit: 5000 }),
				(d) => kind === type && y === year && ((entries = d.results), (years = mergeYears(d.years)))
			);
			if (kind !== type || y !== year) return;
			entries = data.results;
			years = mergeYears(data.years);
			if (initial && !touched && kind !== 'game' && !data.results.some((e) => status === 'All' || e.status === status)) {
				// The default status is empty: open on the first one that has titles.
				status = STATUSES.find((s) => data.results.some((e) => e.status === s)) ?? 'All';
			}
		} catch {
			if (!entries) failed = true;
		}
	}

	/** The year list shrinks when a year is chosen; keep the full list so the user can switch back. */
	const mergeYears = (next: number[]) => (year ? years : next);

	$effect(() => {
		if (!valid) return;
		void type;
		void year;
		load(true);
		return onSynced(() => load());
	});

	async function loadSeasons() {
		const kind = type;
		try {
			const data = await get<{ results: SeasonFinished[] }>(
				'/library/seasons' + qs({ media_type: kind }),
				(d) => kind === type && (seasons = d.results)
			);
			if (kind === type) seasons = data.results;
		} catch {
			if (!seasons) seasons = [];
		}
	}
	$effect(() => {
		if (!valid || !seasonView) return;
		void type;
		seasons = null;
		loadSeasons();
		return onSynced(() => loadSeasons());
	});
	const seasonGroups = $derived.by(() => {
		const q = query.trim().toLowerCase();
		const out: { label: string; list: SeasonFinished[] }[] = [];
		for (const s of seasons ?? []) {
			if (q && !s.item.title.toLowerCase().includes(q)) continue;
			const label = s.end_date ? s.end_date.slice(0, 4) : 'No finish date';
			if (year && label !== year) continue;
			if (out.at(-1)?.label !== label) out.push({ label, list: [] });
			out.at(-1)!.list.push(s);
		}
		return out;
	});
	const seasonLabel = (s: SeasonFinished) =>
		s.season_title && !/^season \d+$/i.test(s.season_title) ? `S${s.season_number} · ${s.season_title}` : `Season ${s.season_number}`;

	// Re-page when any filter changes.
	$effect(() => {
		void status;
		void sort;
		void query;
		void year;
		shown = PAGE;
	});

	const searched = $derived.by(() => {
		const q = query.trim().toLowerCase();
		return (entries ?? []).filter((e) => !q || e.item.title.toLowerCase().includes(q));
	});
	const counts = $derived.by(() => {
		const map: Record<string, number> = { All: searched.length };
		for (const e of searched) map[e.status] = (map[e.status] ?? 0) + 1;
		return map;
	});

	const pct = (e: Entry) => (e.max_progress ? e.progress / e.max_progress : e.progress ? 0.0001 : 0);
	const when = (iso: string | null) => (iso ? Date.parse(iso) : -Infinity);
	const sorted = $derived.by(() => {
		const list = searched.filter((e) => isGame || status === 'All' || e.status === status);
		const by: Record<string, (a: Entry, b: Entry) => number> = {
			title: (a, b) => a.item.title.localeCompare(b.item.title, undefined, { sensitivity: 'base', numeric: true }),
			score: (a, b) => (b.score ?? -1) - (a.score ?? -1),
			progress: (a, b) => pct(b) - pct(a) || b.progress - a.progress,
			start_date: (a, b) => when(b.start_date) - when(a.start_date),
			end_date: (a, b) => when(b.end_date) - when(a.end_date),
			year: (a, b) => when(yearDate(b)) - when(yearDate(a)) || b.id - a.id,
			activity: (a, b) =>
				when(b.progressed_at ?? b.created_at) - when(a.progressed_at ?? a.created_at) || b.id - a.id
		};
		return list.sort(by[sort] ?? by.activity);
	});
	const visible = $derived(sorted.slice(0, shown));

	/** The date that files an entry under a year: finished, or added for games. */
	const yearDate = (e: Entry) => (yv.added ? e.created_at : e.end_date);
	/** With the "year" sort the list is split under year headings (newest first). */
	const groups = $derived.by(() => {
		if (sort !== 'year') return [{ label: '', entries: visible }];
		const out: { label: string; entries: Entry[] }[] = [];
		for (const e of sorted) {
			const d = yearDate(e);
			const label = d ? d.slice(0, 4) : yv.missing;
			if (out.at(-1)?.label !== label) out.push({ label, entries: [] });
			out.at(-1)!.entries.push(e);
		}
		return out;
	});

	function more(node: HTMLElement) {
		const observer = new IntersectionObserver((hits) => hits[0].isIntersecting && (shown += PAGE), {
			rootMargin: '600px'
		});
		observer.observe(node);
		return { destroy: () => observer.disconnect() };
	}

	// Shows keep their progress per season (the list endpoint has no total of watched episodes), so only
	// count-based kinds get a progress line here.
	function subtitle(e: Entry) {
		if (isGame) return '';
		if (hasEpisodes(e.media_type, e.item.source)) return e.status;
		if (e.status === 'In progress' && e.media_type !== 'movie' && e.media_type !== 'anime_movie') {
			if (e.max_progress) return `${Math.round((e.progress / e.max_progress) * 100)}% · ${progressLabel(e.media_type, e.progress, e.max_progress)}`;
			return e.progress ? progressLabel(e.media_type, e.progress, null) : e.status;
		}
		return e.status;
	}
	const hasFilters = $derived(!!query || !!year);
</script>

<svelte:head><title>{meta.plural} · Encompass</title></svelte:head>

{#if !valid}
	<EmptyState title="Unknown section" description="That media type does not exist.">
		{#snippet actions()}<Button href="/library" variant="primary">Back to Library</Button>{/snippet}
	</EmptyState>
{:else}
	<PullToRefresh onrefresh={() => load()}>
		<header class="flex items-center gap-3">
			<span
				class="grid size-10 shrink-0 place-items-center rounded-xl"
				style="background: color-mix(in oklab, {meta.accent} 16%, transparent); color: {meta.accent}"
			>
				<meta.icon class="size-5" />
			</span>
			<div class="min-w-0 flex-1">
				<h1 class="truncate text-xl font-semibold tracking-tight sm:text-2xl">{meta.plural}</h1>
				<p class="h-4 text-[0.8125rem] text-faint">
					{#if seasonView}{#if seasons}{seasonGroups.reduce((n, g) => n + g.list.length, 0)} seasons finished{/if}
					{:else if entries}{counts.All} {year ? `active in ${year}` : 'tracked'}{/if}
				</p>
			</div>
			<IconButton
				icon={view === 'grid' ? List : LayoutGrid}
				label={view === 'grid' ? 'Switch to list view' : 'Switch to grid view'}
				onclick={() => setView(view === 'grid' ? 'list' : 'grid')}
			/>
			<IconButton icon={ArrowDownUp} label="Sort" onclick={() => (sortOpen = true)} />
			<IconButton icon={Plus} label="Find {meta.plural.toLowerCase()} to track" variant="brand" href="/search?type={type}" />
		</header>

		<div class="mt-3 flex gap-2">
			<label class="relative min-w-0 flex-1">
				<span class="sr-only">Search {meta.plural}</span>
				<Search class="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-faint" />
				<input
					bind:value={query}
					type="search"
					enterkeyhint="search"
					placeholder="Search {meta.plural.toLowerCase()}"
					class="h-11 w-full rounded-xl border border-line bg-surface pr-10 pl-9 outline-none transition focus:border-line-strong"
				/>
				{#if query}
					<button
						type="button"
						class="absolute top-0 right-0 grid size-11 place-items-center text-faint hover:text-ink"
						aria-label="Clear search"
						onclick={() => (query = '')}><X class="size-4" /></button
					>
				{/if}
			</label>
			{#if years.length}
				<select
					bind:value={year}
					aria-label="Filter by year"
					class="h-11 shrink-0 rounded-xl border border-line bg-surface px-3 outline-none focus:border-line-strong"
				>
					<option value="">All years</option>
					{#each years as y (y)}<option value={String(y)}>{y}</option>{/each}
				</select>
			{/if}
		</div>

		{#if hasSeasons}
			<div class="mt-3 inline-flex rounded-xl border border-line bg-surface p-0.5 text-sm" role="radiogroup" aria-label="Show">
				{#each [[false, 'Shows'], [true, 'Seasons']] as const as [v, label] (label)}
					<button
						type="button"
						role="radio"
						aria-checked={bySeason === v}
						class="min-h-9 rounded-[10px] px-3 font-medium {bySeason === v ? 'bg-ink/10 text-ink' : 'text-muted hover:text-ink'}"
						onclick={() => setBySeason(v)}>{label}</button
					>
				{/each}
			</div>
		{/if}
		{#if !isGame && !seasonView}<ChipRow label="Status">
			{#each CHIPS as s (s)}
				<Chip
					selected={status === s}
					color={s === 'All' ? undefined : STATUS_COLOR[s]}
					count={entries ? (counts[s] ?? 0) : undefined}
					onclick={() => {
						status = s;
						touched = true;
					}}>{s}</Chip
				>
			{/each}
		</ChipRow>{/if}

		<div class="mt-3">
			{#if seasonView}
				{#if !seasons}
					<PosterGrid>
						{#each Array(12) as _, i (i)}
							<div>
								<Skeleton class="aspect-[2/3] w-full" />
								<Skeleton shape="text" class="mt-2" lines={2} />
							</div>
						{/each}
					</PosterGrid>
				{:else if seasonGroups.length === 0}
					<EmptyState
						icon={meta.icon}
						title={query || year ? 'No matches' : 'No finished seasons yet'}
						description="Seasons show up here, under the year you finished them."
					/>
				{:else}
					{#each seasonGroups as g, i (g.label)}
						{@render yearHeader(g.label, g.list.length, i)}
						{#if !isOpen(g.label, i)}
							<!-- folded -->
						{:else if view === 'grid'}
							<PosterGrid>
								{#each g.list as s (`${s.entry_id}:${s.season_number}`)}
									<PosterCard
										href={titleHref(s.item, s.item.media_type, s.entry_id)}
										src={s.image}
										title={s.item.title}
										type={s.item.media_type}
										subtitle={seasonLabel(s)}
										score={s.score}
									/>
								{/each}
							</PosterGrid>
						{:else}
							<ul class="divide-y divide-line overflow-hidden rounded-[var(--radius-card)] border border-line bg-surface">
								{#each g.list as s (`${s.entry_id}:${s.season_number}`)}
									<li>
										<ListRow href={titleHref(s.item, s.item.media_type, s.entry_id)} title={s.item.title} subtitle={seasonLabel(s)}>
											{#snippet leading()}
												<Cover src={s.image} title={s.item.title} type={s.item.media_type} class="aspect-[2/3] w-10 shrink-0 rounded-md" />
											{/snippet}
										</ListRow>
									</li>
								{/each}
							</ul>
						{/if}
					{/each}
				{/if}
			{:else if failed}
				<EmptyState title="Could not load {meta.plural.toLowerCase()}" description="Check your connection and try again.">
					{#snippet actions()}<Button variant="primary" onclick={() => load(true)}>Retry</Button>{/snippet}
				</EmptyState>
			{:else if !entries}
				{#if view === 'grid'}
					<PosterGrid>
						{#each Array(12) as _, i (i)}
							<div>
								<Skeleton class="aspect-[2/3] w-full" />
								<Skeleton shape="text" class="mt-2" lines={2} />
							</div>
						{/each}
					</PosterGrid>
				{:else}
					<div class="space-y-2">
						{#each Array(8) as _, i (i)}<Skeleton class="h-16 w-full" />{/each}
					</div>
				{/if}
			{:else if sorted.length === 0}
				{#if entries.length === 0 && !hasFilters}
					<EmptyState
						icon={meta.icon}
						title="No {meta.plural.toLowerCase()} yet"
						description="Search for something and start tracking it."
					>
						{#snippet actions()}<Button variant="primary" href="/search?type={type}" icon={Plus}>Find something</Button>{/snippet}
					</EmptyState>
				{:else}
					<EmptyState
						icon={Search}
						title={isGame || status === 'All' || hasFilters ? 'No matches' : `Nothing ${status.toLowerCase()}`}
						description={hasFilters ? 'Try a different search or year.' : 'Pick another status above.'}
					>
						{#snippet actions()}
							{#if status !== 'All'}<Button onclick={() => (status = 'All')}>Show all</Button>{/if}
							{#if hasFilters}<Button onclick={() => ((query = ''), (year = ''))}>Clear filters</Button>{/if}
						{/snippet}
					</EmptyState>
				{/if}
			{:else}
				{#each groups as g, i (g.label)}
					{#if g.label}{@render yearHeader(g.label, g.entries.length, i)}{/if}
					{#if g.label && !isOpen(g.label, i)}
						<!-- folded -->
					{:else if view === 'grid'}
						<PosterGrid>
							{#each g.entries as entry (entry.id)}
								<PosterCard
									href={titleHref(entry.item, entry.media_type, entry.id)}
									src={entry.item.image}
									title={entry.item.title}
									type={entry.media_type}
									status={isGame ? undefined : entry.status}
									subtitle={subtitle(entry)}
									score={entry.score}
									release={entry.item.release_status}
								/>
							{/each}
						</PosterGrid>
					{:else}
						<ul class="divide-y divide-line overflow-hidden rounded-[var(--radius-card)] border border-line bg-surface">
							{#each g.entries as entry (entry.id)}
								<li>
									<ListRow
										href={titleHref(entry.item, entry.media_type, entry.id)}
										title={entry.item.title}
										subtitle={subtitle(entry)}
									>
										{#snippet leading()}
											<Cover
												src={entry.item.image}
												title={entry.item.title}
												type={entry.media_type}
												class="aspect-[2/3] w-10 shrink-0 rounded-md"
											/>
										{/snippet}
										{#snippet trailing()}
											<span class="flex shrink-0 items-center gap-2 text-[0.8125rem] text-muted">
												{#if entry.score}
													<span class="flex items-center gap-1 tabular-nums">
														<Star class="size-3.5 fill-amber-300 text-amber-300" />{entry.score}
													</span>
												{/if}
											</span>
										{/snippet}
									</ListRow>
								</li>
							{/each}
						</ul>
					{/if}
				{/each}
			{/if}

			{#if entries && sort !== 'year' && visible.length < sorted.length}
				<div use:more class="h-16"></div>
			{/if}
		</div>
	</PullToRefresh>

	<SortSheet bind:open={sortOpen} bind:value={sort} kind={type} onpick={() => (touched = true)} />
{/if}

{#snippet yearHeader(label: string, count: number, i: number)}
	<h2 class="mt-3 first:mt-1">
		<button
			type="button"
			class="flex min-h-11 w-full items-center gap-2 rounded-xl px-1 text-left text-lg font-semibold tracking-tight hover:bg-ink/5"
			aria-expanded={isOpen(label, i)}
			onclick={() => toggleYear(label, i)}
		>
			<ChevronDown class="size-4 shrink-0 text-faint transition-transform {isOpen(label, i) ? '' : '-rotate-90'}" />
			{label}
			<span class="ml-auto text-[0.8125rem] font-normal text-faint tabular-nums">{count}</span>
		</button>
	</h2>
{/snippet}
