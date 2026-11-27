<script lang="ts">
	import { CalendarDays, CalendarPlus, ChevronLeft, ChevronRight, Tv } from '@lucide/svelte';
	import { get, qs } from '#lib/api.ts';
	import ReleaseBanner from '#lib/components/ReleaseBanner.svelte';
	import ReleaseCard from '#lib/components/ReleaseCard.svelte';
	import SeasonPicker, { type SeasonRef } from '#lib/components/SeasonPicker.svelte';
	import { isoDate, parseDay } from '#lib/components/reading/format.ts';
	import type { CalendarEvent } from '#lib/components/reading/types.ts';
	import { MEDIA, titleHref } from '#lib/media.ts';
	import { loadSettings, settings } from '#lib/settings.svelte.ts';
	import { toast } from '#lib/toast.svelte.ts';
	import type { SearchResult } from '#lib/types.ts';
	import { Button, CopyField, EmptyState, IconButton, PosterCard, PosterGrid, PullToRefresh, Sheet, Skeleton, Tabs } from '#lib/ui/index.ts';

	let view = $state('agenda');
	let events = $state<CalendarEvent[] | null>(null);
	let subscribeOpen = $state(false);

	const today = isoDate(new Date());
	const monday = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate() - ((d.getDay() + 6) % 7));
	const plusDays = (d: Date, n: number) => new Date(d.getFullYear(), d.getMonth(), d.getDate() + n);
	const thisWeek = monday(new Date());
	// The agenda shows one week (Monday to Sunday) at a time.
	let week = $state(monday(new Date()));
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
		return { start: isoDate(week), end: isoDate(plusDays(week, 6)) };
	}

	interface SeasonalTitle extends SearchResult {
		episodes: number | null;
		score: number | null;
		studio?: string;
		start_date: string | null;
	}
	let seasons = $state<SeasonRef[] | null>(null);
	let picked_season = $state<SeasonRef | null>(null);
	let current_season = $state<SeasonRef | null>(null);
	let titles = $state<SeasonalTitle[] | null>(null);
	let seasonalError = $state(false);

	async function loadSeasons() {
		try {
			const d = await get<{ current: SeasonRef; seasons: SeasonRef[] }>('/seasonal/seasons');
			seasons = d.seasons;
			current_season = d.current;
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
	/** Where the chosen season sits in the list, for the arrows. */
	const seasonAt = $derived(seasons?.findIndex((x) => x.label === picked_season?.label) ?? -1);
	function stepSeason(by: number) {
		const next = seasons?.[seasonAt + by];
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
		void week;
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
	/** The week's releases in order (the list may hold the week before while the next loads). */
	const inWeek = $derived.by(() => {
		const { start, end } = { start: isoDate(week), end: isoDate(plusDays(week, 6)) };
		return byDay.flatMap(([iso, list]) => (iso >= start && iso <= end ? list : []));
	});
	// The year is left out on a phone, where the title shares its row with the arrows.
	const weekDates = (withYear: boolean) => {
		const end = plusDays(week, 6);
		const year = withYear && end.getFullYear() !== thisWeek.getFullYear() ? ({ year: 'numeric' } as const) : {};
		const short = { day: 'numeric', month: 'short' } as const;
		// A range across New Year always names both years; spell it out without them.
		if (!withYear && end.getFullYear() !== week.getFullYear())
			return `${week.toLocaleDateString(undefined, short)} – ${end.toLocaleDateString(undefined, short)}`;
		return new Intl.DateTimeFormat(undefined, { ...short, ...year }).formatRange(week, end);
	};
	const weekDays = $derived(byDay.filter(([iso]) => iso >= isoDate(week) && iso <= isoDate(plusDays(week, 6))));
	const weekName = $derived.by(() => {
		const away = Math.round((week.getTime() - thisWeek.getTime()) / (7 * 864e5));
		if (away === 0) return 'This week';
		if (away === 1) return 'Next week';
		if (away === -1) return 'Last week';
		return '';
	});
	let seeking = $state(false);
	/** From an empty week, go to the week of the next release after it. */
	async function nextRelease() {
		seeking = true;
		try {
			const from = plusDays(week, 7);
			const list = await get<CalendarEvent[]>('/calendar' + qs({ start: isoDate(from), end: isoDate(plusDays(from, 365)) }));
			const first = list.map((e) => e.date).sort()[0];
			if (first) week = monday(parseDay(first));
			else toast('Nothing announced in the next year', { kind: 'info' });
		} catch {
			toast('Could not look ahead. Check your connection.', { kind: 'error' });
		} finally {
			seeking = false;
		}
	}
	const when = (iso: string) => {
		if (iso === today) return 'Today';
		const d = parseDay(iso);
		if (Math.round((d.getTime() - parseDay(today).getTime()) / 864e5) === 1) return 'Tomorrow';
		return d.toLocaleDateString(undefined, { weekday: 'short', month: 'short', day: 'numeric' });
	};

	const monthName = $derived(month.toLocaleDateString(undefined, { month: 'long', year: 'numeric' }));
	/** The count beside the title: what the chosen view is showing. */
	const summary = $derived.by(() => {
		const count = (n: number, one: string) => `${n} ${one}${n === 1 ? '' : 's'}`;
		if (view === 'seasonal') return titles ? count(titles.length, 'title') : '';
		if (!events) return '';
		if (view === 'agenda') return count(inWeek.length, 'release');
		const prefix = isoDate(month).slice(0, 7);
		return count(events.filter((e) => e.date.startsWith(prefix)).length, 'release');
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

<PullToRefresh onrefresh={load}>
	<header class="mb-5">
		<Tabs
			label="Upcoming view"
			variant="pills"
			bind:value={view}
			options={[
				{ value: 'agenda', label: 'Agenda' },
				{ value: 'month', label: 'Month' },
				{ value: 'seasonal', label: 'Seasonal anime' }
			]}
		/>
		<!-- On a phone the count drops under the title, so the arrows stay beside it. -->
		<div class="mt-2 grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-2 sm:flex sm:gap-x-4">
			{#if view === 'seasonal' && picked_season && seasons}
				<SeasonPicker {seasons} value={picked_season} current={current_season} onpick={(s) => (picked_season = s)} />
			{:else}
				<h1 class="flex min-h-11 items-center text-2xl font-semibold tracking-tight whitespace-nowrap sm:text-3xl">
					{#if view === 'month'}<span class="sm:hidden">{month.toLocaleDateString(undefined, { month: 'short', year: 'numeric' })}</span><span class="max-sm:hidden">{monthName}</span>
					{:else if view === 'agenda'}
						{#if weekName}{weekName}{:else}<span class="sm:hidden">{weekDates(false)}</span><span class="max-sm:hidden">{weekDates(true)}</span>{/if}
					{:else}Upcoming{/if}
				</h1>
			{/if}
			<div class="order-last col-span-2 flex min-h-5 items-center gap-3 text-[0.8125rem] sm:order-none">
				<p class="text-faint" aria-live="polite">{summary}</p>
				{#if view === 'agenda' && week.getTime() !== thisWeek.getTime()}
					<button type="button" class="-my-3 min-h-11 font-medium text-brand sm:hidden" onclick={() => (week = thisWeek)}>Back to this week</button>
				{/if}
			</div>
			<div class="flex items-center gap-1.5 justify-self-end sm:ml-auto sm:gap-2">
				{#if view === 'seasonal'}
					<IconButton variant="surface" icon={ChevronLeft} label="Previous season" disabled={seasonAt <= 0} onclick={() => stepSeason(-1)} />
					<IconButton variant="surface" icon={ChevronRight} label="Next season" disabled={seasonAt < 0 || seasonAt >= (seasons?.length ?? 0) - 1} onclick={() => stepSeason(1)} />
					<span class="mx-1 h-6 w-px bg-line max-sm:hidden" aria-hidden="true"></span>
				{:else if view === 'month'}
					<IconButton variant="surface" icon={ChevronLeft} label="Previous month" onclick={() => shift(-1)} />
					<IconButton variant="surface" icon={ChevronRight} label="Next month" onclick={() => shift(1)} />
					<span class="mx-1 h-6 w-px bg-line max-sm:hidden" aria-hidden="true"></span>
				{:else}
					{#if week.getTime() !== thisWeek.getTime()}
						<Button variant="ghost" class="max-sm:hidden" onclick={() => (week = thisWeek)}>Today</Button>
					{/if}
					<IconButton variant="surface" icon={ChevronLeft} label="Previous week" onclick={() => (week = plusDays(week, -7))} />
					<IconButton variant="surface" icon={ChevronRight} label="Next week" onclick={() => (week = plusDays(week, 7))} />
					<span class="mx-1 h-6 w-px bg-line max-sm:hidden" aria-hidden="true"></span>
				{/if}
				<button
					type="button"
					class="inline-flex min-h-11 min-w-11 items-center justify-center gap-2 rounded-full bg-ink px-3 text-sm font-semibold text-canvas transition hover:opacity-90 active:scale-95 sm:px-5"
					onclick={() => (subscribeOpen = true)}
				>
					<CalendarPlus class="size-4" /><span class="max-sm:sr-only">Subscribe</span>
				</button>
			</div>
		</div>
	</header>

	{#if view === 'seasonal'}
		<section aria-label="Seasonal anime">
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
			{#if inWeek.length === 0}
				<EmptyState icon={CalendarDays} title="Nothing this week" description="Episodes and releases for what you track show up here.">
					{#snippet actions()}<Button variant="primary" loading={seeking} onclick={nextRelease}>Next release</Button>{/snippet}
				</EmptyState>
			{:else}
				<ol class="space-y-5">
					{#each weekDays as [iso, list] (iso)}
						{@const day = parseDay(iso)}
						<li class="flex gap-3 sm:gap-6" aria-label={day.toLocaleDateString(undefined, { weekday: 'long', month: 'long', day: 'numeric' })}>
							<div class="w-14 shrink-0 pt-1 text-center sm:w-28 {iso < today ? 'text-muted' : ''}">
								<p class="text-sm font-bold tracking-wide uppercase sm:hidden {iso === today ? 'text-brand' : ''}">
									{day.toLocaleDateString(undefined, { weekday: 'short' })}
								</p>
								<p class="hidden text-lg font-bold sm:block {iso === today ? 'text-brand' : ''}">
									{day.toLocaleDateString(undefined, { weekday: 'long' })}
								</p>
								<p class="text-3xl leading-tight font-bold tabular-nums sm:text-5xl {iso === today ? 'text-brand' : ''}">{day.getDate()}</p>
								<p class="text-xs text-faint">
									{iso === today ? 'Today' : day.toLocaleDateString(undefined, { month: 'short' })}
								</p>
							</div>
							<div class="grid min-w-0 flex-1 gap-3 {list.length > 1 ? 'xl:grid-cols-2' : ''}">
								{#each list as e, i (e.item_id + ':' + e.season + ':' + e.episode + ':' + i)}<ReleaseBanner event={e} />{/each}
							</div>
						</li>
					{/each}
				</ol>
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
