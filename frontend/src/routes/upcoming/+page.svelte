<script lang="ts">
	import type { Snippet } from 'svelte';
	import { CalendarDays, CalendarPlus, ChevronLeft, ChevronRight, Tv } from '@lucide/svelte';
	import { get, qs } from '#lib/api.ts';
	import ReleaseCard from '#lib/components/ReleaseCard.svelte';
	import SectionHead from '#lib/components/SectionHeader.svelte';
	import { isoDate, parseDay } from '#lib/components/reading/format.ts';
	import type { CalendarEvent } from '#lib/components/reading/types.ts';
	import { MEDIA, titleHref } from '#lib/media.ts';
	import { loadSettings, settings } from '#lib/settings.svelte.ts';
	import type { SearchResult } from '#lib/types.ts';
	import { Button, CopyField, EmptyState, IconButton, PosterCard, PosterGrid, PullToRefresh, Sheet, Skeleton, Tabs } from '#lib/ui/index.ts';

	let view = $state('agenda');
	let events = $state<CalendarEvent[] | null>(null);
	let showPast = $state(false);
	let subscribeOpen = $state(false);

	const today = isoDate(new Date());
	let month = $state(new Date(new Date().getFullYear(), new Date().getMonth(), 1));
	let picked = $state<string | null>(null);

	function range() {
		if (view === 'month') {
			const first = new Date(month);
			const gridStart = new Date(first);
			gridStart.setDate(1 - ((first.getDay() + 6) % 7));
			const gridEnd = new Date(gridStart);
			gridEnd.setDate(gridStart.getDate() + 41);
			return { start: isoDate(gridStart), end: isoDate(gridEnd) };
		}
		const s = new Date();
		s.setDate(s.getDate() - (showPast ? 30 : 0));
		const e = new Date();
		e.setDate(e.getDate() + 90);
		return { start: isoDate(s), end: isoDate(e) };
	}

	interface SeasonRef {
		season: string;
		year: number;
		label: string;
	}
	interface SeasonalTitle extends SearchResult {
		episodes: number | null;
		score: number | null;
		studio?: string;
		start_date: string | null;
	}
	let seasons = $state<SeasonRef[] | null>(null);
	let picked_season = $state<SeasonRef | null>(null);
	let titles = $state<SeasonalTitle[] | null>(null);
	let seasonalError = $state(false);

	async function loadSeasons() {
		try {
			const d = await get<{ current: SeasonRef; seasons: SeasonRef[] }>('/seasonal/seasons');
			seasons = d.seasons;
			picked_season ??= d.seasons.find((x) => x.label === d.current.label) ?? d.seasons[0] ?? null;
		} catch {
			seasonalError = true;
			seasons = [];
		}
	}
	async function loadTitles(pick: SeasonRef) {
		titles = null;
		seasonalError = false;
		try {
			const d = await get<{ results: SeasonalTitle[] }>('/seasonal' + qs({ season: pick.season, year: pick.year }));
			if (picked_season?.label === pick.label) titles = d.results;
		} catch {
			if (picked_season?.label === pick.label) {
				seasonalError = true;
				titles = [];
			}
		}
	}
	$effect(() => {
		if (view !== 'seasonal') return;
		if (!seasons) void loadSeasons();
	});
	$effect(() => {
		if (view === 'seasonal' && picked_season) void loadTitles(picked_season);
	});
	// A year picker plus four season tabs: nothing to scroll sideways, however many years there are.
	const SEASONS = ['winter', 'spring', 'summer', 'fall'];
	const years = $derived([...new Set((seasons ?? []).map((s) => s.year))]);
	const seasonOptions = $derived(
		SEASONS.map((name) => ({
			value: name,
			label: name[0].toUpperCase() + name.slice(1),
			disabled: !seasons?.some((s) => s.year === picked_season?.year && s.season === name)
		}))
	);
	function pickSeason(name: string) {
		const next = seasons?.find((s) => s.year === picked_season?.year && s.season === name);
		if (next) picked_season = next;
	}
	/** Same season in another year, or that year's closest one at either end of the range. */
	function pickYear(year: number) {
		const inYear = seasons?.filter((s) => s.year === year) ?? [];
		const at = SEASONS.indexOf(picked_season?.season ?? 'winter');
		const next = [...inYear].sort((a, b) => Math.abs(SEASONS.indexOf(a.season) - at) - Math.abs(SEASONS.indexOf(b.season) - at))[0];
		if (next) picked_season = next;
	}

	async function load() {
		if (view === 'seasonal') {
			seasons = null;
			titles = null;
			return;
		}
		const { start, end } = range();
		try {
			const list = await get<CalendarEvent[]>('/calendar' + qs({ start, end }));
			if (range().start === start) events = list;
		} catch {
			events ??= [];
		}
	}
	$effect(() => {
		void view;
		void month;
		void showPast;
		if (view === 'seasonal') return;
		events = null;
		load();
	});
	$effect(() => {
		loadSettings();
	});

	const shown = $derived(events ?? []);
	const byDay = $derived.by(() => {
		const map = new Map<string, CalendarEvent[]>();
		for (const e of shown) map.set(e.date, [...(map.get(e.date) ?? []), e]);
		return [...map.entries()].sort(([a], [b]) => a.localeCompare(b));
	});

	const formatShort = (iso: string) => parseDay(iso).toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
	const heading = (iso: string) => {
		if (iso === today) return 'Today';
		const d = parseDay(iso);
		const t = parseDay(today);
		if (Math.round((d.getTime() - t.getTime()) / 864e5) === 1) return 'Tomorrow';
		return d.toLocaleDateString(undefined, { weekday: 'long', month: 'short', day: 'numeric' });
	};
	// The agenda is grouped by week, then month, so cards fill the grid as they do on Now.
	const byPeriod = $derived.by(() => {
		const t = parseDay(today);
		const sunday = new Date(t);
		sunday.setDate(t.getDate() + (6 - ((t.getDay() + 6) % 7)));
		const nextSunday = new Date(sunday);
		nextSunday.setDate(sunday.getDate() + 7);
		const period = (iso: string) => {
			if (iso < today) return 'Last 30 days';
			if (iso <= isoDate(sunday)) return 'This week';
			if (iso <= isoDate(nextSunday)) return 'Next week';
			const d = parseDay(iso);
			return d.toLocaleDateString(undefined, { month: 'long', ...(d.getFullYear() === t.getFullYear() ? {} : { year: 'numeric' }) });
		};
		const map = new Map<string, CalendarEvent[]>();
		for (const [iso, list] of byDay) map.set(period(iso), [...(map.get(period(iso)) ?? []), ...list]);
		return [...map.entries()];
	});
	const when = (iso: string) => {
		if (iso === today) return 'Today';
		const d = parseDay(iso);
		if (Math.round((d.getTime() - parseDay(today).getTime()) / 864e5) === 1) return 'Tomorrow';
		return d.toLocaleDateString(undefined, { weekday: 'short', month: 'short', day: 'numeric' });
	};

	const monthName = $derived(month.toLocaleDateString(undefined, { month: 'long', year: 'numeric' }));
	/** The line under the title: what the chosen view is showing. */
	const summary = $derived.by(() => {
		const count = (n: number, one: string) => `${n} ${one}${n === 1 ? '' : 's'}`;
		if (view === 'seasonal') return picked_season ? `${picked_season.label}${titles ? ` · ${count(titles.length, 'title')}` : ''}` : '';
		if (!events) return '';
		if (view === 'agenda') return `${events.filter((e) => e.date >= today).length} in the next 90 days`;
		const prefix = isoDate(month).slice(0, 7);
		return count(events.filter((e) => e.date.startsWith(prefix)).length, 'release'); // the month is named beside it
	});

	// Month grid cells (6 weeks, Monday first).
	const cells = $derived.by(() => {
		const first = new Date(month);
		const start = new Date(first);
		start.setDate(1 - ((first.getDay() + 6) % 7));
		return Array.from({ length: 42 }, (_, i) => {
			const d = new Date(start);
			d.setDate(start.getDate() + i);
			const iso = isoDate(d);
			return { iso, day: d.getDate(), inMonth: d.getMonth() === first.getMonth() };
		});
	});
	const eventsOn = (iso: string) => shown.filter((e) => e.date === iso);
	const shift = (n: number) => {
		month = new Date(month.getFullYear(), month.getMonth() + n, 1);
		picked = null;
	};

	const feedUrl = $derived(settings.data ? location.origin + settings.data.calendar_url_path : '');
	const webcal = $derived(feedUrl.replace(/^https?:/, 'webcal:'));
</script>


<svelte:head><title>Upcoming · Axiom</title></svelte:head>

{#snippet stepper(what: string, previous: (() => void) | null, next: (() => void) | null, label: Snippet)}
	<div class="flex shrink-0 items-center justify-between gap-1 rounded-xl border border-line bg-surface p-1" role="group" aria-label={what}>
		<IconButton icon={ChevronLeft} label="Previous {what}" disabled={!previous} onclick={() => previous?.()} class="!rounded-lg" />
		{@render label()}
		<IconButton icon={ChevronRight} label="Next {what}" disabled={!next} onclick={() => next?.()} class="!rounded-lg" />
	</div>
{/snippet}

{#snippet monthLabel()}
	<span class="min-w-36 text-center text-sm font-semibold" aria-live="polite">{monthName}</span>
{/snippet}

{#snippet yearLabel()}
	<select
		class="h-11 min-w-24 cursor-pointer appearance-none rounded-lg bg-transparent px-3 text-center text-sm font-semibold tabular-nums hover:bg-ink/5"
		aria-label="Year"
		value={picked_season?.year}
		onchange={(e) => pickYear(Number(e.currentTarget.value))}
	>
		{#each years as y (y)}<option value={y}>{y}</option>{/each}
	</select>
{/snippet}

<PullToRefresh onrefresh={load}>
	<header class="mb-4 flex items-end justify-between gap-3">
		<div class="min-w-0">
			<h1 class="truncate text-2xl font-semibold tracking-tight sm:text-3xl">Upcoming</h1>
			<p class="mt-1 h-5 truncate text-[0.8125rem] text-faint">{summary}</p>
		</div>
		<Button variant="secondary" icon={CalendarPlus} onclick={() => (subscribeOpen = true)} class="shrink-0">Subscribe</Button>
	</header>

	<div class="mb-6 flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
		<Tabs
			label="Upcoming view"
			full
			class="sm:!w-fit"
			bind:value={view}
			options={[
				{ value: 'agenda', label: 'Agenda' },
				{ value: 'month', label: 'Month' },
				{ value: 'seasonal', label: 'Seasonal anime' }
			]}
		/>

		{#if view === 'seasonal' && picked_season}
			{@const year = picked_season.year}
			<div class="flex flex-col gap-3 sm:flex-row sm:items-center">
				{@render stepper('year', year > years[0] ? () => pickYear(year - 1) : null, year < years[years.length - 1] ? () => pickYear(year + 1) : null, yearLabel)}
				<Tabs label="Season" full class="sm:!w-fit" value={picked_season.season} options={seasonOptions} onchange={pickSeason} />
			</div>
		{:else if view === 'month'}
			{@render stepper('month', () => shift(-1), () => shift(1), monthLabel)}
		{:else if view === 'agenda' && !showPast}
			<Button variant="secondary" onclick={() => (showPast = true)} class="self-start lg:self-auto">Show the last 30 days</Button>
		{/if}
	</div>

	{#if view === 'seasonal'}
		<section aria-label="Seasonal anime">
			{#if seasons === null}
				<Skeleton class="mb-4 h-10 w-full" />
			{/if}
			{#if titles === null}
				<div class="grid grid-cols-3 gap-3 sm:grid-cols-4 lg:grid-cols-6" aria-hidden="true">{#each Array(12) as _, i (i)}<Skeleton class="aspect-[2/3] w-full" />{/each}</div>
			{:else if titles.length === 0}
				<EmptyState
					icon={Tv}
					title={seasonalError ? 'Could not load this season' : 'No anime announced yet'}
					description={seasonalError ? 'AniList may be down or rate-limited. Try again in a moment.' : 'Nothing is listed for this season so far.'}
				>
					{#snippet actions()}{#if seasonalError && picked_season}<Button variant="primary" onclick={() => picked_season && loadTitles(picked_season)}>Retry</Button>{/if}{/snippet}
				</EmptyState>
			{:else}
				<PosterGrid>
					{#each titles as t (t.source + t.media_id)}
						<PosterCard
							href={titleHref(t, t.media_type)}
							src={t.image}
							title={t.title}
							type={t.media_type}
							score={t.score ? Math.round(t.score) / 10 : null}
							subtitle={[t.studio, t.start_date ? formatShort(t.start_date) : ''].filter(Boolean).join(' · ') || (t.episodes ? `${t.episodes} episodes` : 'TBA')}
						/>
					{/each}
				</PosterGrid>
			{/if}
		</section>
	{:else if events === null}
		<div class="mt-4 space-y-3" aria-hidden="true"><Skeleton class="h-6 w-40" /><Skeleton class="h-40 w-full" /></div>
	{:else if view === 'agenda'}
		<div>
			{#if byDay.length === 0}
				<EmptyState icon={CalendarDays} title="Nothing coming up" description="Upcoming episodes and releases for what you track will show up here." />
			{:else}
				<div class="space-y-8">
					{#each byPeriod as [period, list] (period)}
						<section aria-label={period}>
							<SectionHead title={period} icon={CalendarDays} accent="var(--color-brand)" count={list.length} />
							<div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
								{#each list as e, i (e.item_id + ':' + e.season + ':' + e.episode + ':' + i)}<ReleaseCard event={e} when={when(e.date)} />{/each}
							</div>
						</section>
					{/each}
				</div>
			{/if}
		</div>
	{:else}
		<div>
			<div class="grid grid-cols-7 gap-1 text-center text-[0.75rem] text-faint" aria-hidden="true">
				{#each ['M', 'T', 'W', 'T', 'F', 'S', 'S'] as d, i (i)}<span>{d}</span>{/each}
			</div>
			<div class="mt-1 grid grid-cols-7 gap-1" role="grid" aria-label="Month">
				{#each cells as c (c.iso)}
					{@const on = eventsOn(c.iso)}
					<button
						type="button"
						role="gridcell"
						aria-label="{c.iso}: {on.length} releases"
						aria-selected={picked === c.iso}
						onclick={() => (picked = picked === c.iso ? null : c.iso)}
						class="flex min-h-12 flex-col items-center justify-start gap-1 rounded-xl border pt-1.5 transition sm:min-h-20 {picked === c.iso
							? 'border-brand bg-brand/10'
							: c.iso === today
								? 'border-line-strong bg-raised'
								: 'border-transparent hover:bg-ink/5'} {c.inMonth ? '' : 'opacity-35'}"
					>
						<span class="text-[0.8125rem] tabular-nums {c.iso === today ? 'font-bold text-brand' : ''}">{c.day}</span>
						{#if on.length}
							<span class="flex flex-wrap justify-center gap-0.5 px-1">
								{#each [...new Set(on.map((e) => e.media_type))].slice(0, 4) as t (t)}
									<span class="size-1.5 rounded-full" style="background: {MEDIA[t].accent}"></span>
								{/each}
							</span>
						{/if}
					</button>
				{/each}
			</div>
			{#if picked}
				{@const list = eventsOn(picked)}
				<section class="mt-4" aria-label="Releases on {picked}">
					<h3 class="mb-1.5 px-1 text-[0.875rem] font-semibold">{heading(picked)}</h3>
					{#if list.length}
						<div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
							{#each list as e, i (i)}<ReleaseCard event={e} />{/each}
						</div>
					{:else}
						<p class="text-sm text-muted">Nothing on this day.</p>
					{/if}
				</section>
			{/if}
		</div>
	{/if}
</PullToRefresh>

<Sheet bind:open={subscribeOpen} title="Subscribe to your calendar">
	<p class="mb-3 text-[0.875rem] text-muted">
		Add this private URL to Google Calendar, Apple Calendar or any app that reads .ics feeds. It updates by itself.
	</p>
	{#if feedUrl}
		<CopyField value={feedUrl} label="calendar URL" />
		<Button class="mt-3" variant="primary" full href={webcal}>Open in calendar app</Button>
		<p class="mt-3 text-[0.75rem] text-faint">Anyone with this URL can see your releases. You can regenerate it in Settings.</p>
	{:else}
		<Skeleton class="h-11 w-full" />
	{/if}
</Sheet>
