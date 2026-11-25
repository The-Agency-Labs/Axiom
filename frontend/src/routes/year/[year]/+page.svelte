<script lang="ts">
	import { BookOpen, CalendarDays, CheckCheck, ChevronLeft, ChevronRight, Clock, Flame, Layers, Star } from '@lucide/svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { get } from '#lib/api.ts';
	import Cover from '#lib/components/Cover.svelte';
	import MonthBars from '#lib/components/MonthBars.svelte';
	import { formatDate, MEDIA, minutesLabel } from '#lib/media.ts';
	import type { MediaType, YearSnapshot } from '#lib/types.ts';

	const year = $derived(Number(page.params.year));
	let data = $state<YearSnapshot | null>(null);
	let years = $state<number[]>([]);
	let error = $state('');

	$effect(() => {
		const current = year;
		data = null;
		error = '';
		get<YearSnapshot>(`/years/${current}`)
			.then((snapshot) => {
				if (current === year) data = snapshot;
			})
			.catch(() => (error = 'Could not build this year.'));
	});

	$effect(() => {
		get<number[]>('/years').then((list) => (years = list));
	});

	const thisYear = new Date().getFullYear();
	const older = $derived(years.filter((y) => y < year).at(0) ?? null);
	const newer = $derived([...years].reverse().find((y) => y > year) ?? (year < thisYear ? year + 1 : null));

	// Sections in the same order as the library, units spelled out.
	const ORDER: MediaType[] = ['tv', 'movie', 'anime', 'manga', 'book', 'game', 'boardgame'];
	const UNIT_WORDS: Partial<Record<MediaType, [string, string]>> = {
		tv: ['episode', 'episodes'],
		movie: ['movie', 'movies'],
		anime: ['episode', 'episodes'],
		manga: ['chapter', 'chapters'],
		book: ['page', 'pages'],
		comic: ['issue', 'issues'],
		game: ['minute', 'minutes'],
		boardgame: ['play', 'plays']
	};
	const sections = $derived(
		ORDER.filter((t) => data?.sections[t]).map((t) => ({ type: t, ...data!.sections[t]! }))
	);
	const unitWord = (t: MediaType, n: number) => (UNIT_WORDS[t] ?? ['', ''])[n === 1 ? 0 : 1];

	const finishedByType = $derived.by(() => {
		const map = new Map<MediaType, YearSnapshot['finished']>();
		for (const f of data?.finished ?? []) map.set(f.media_type, [...(map.get(f.media_type) ?? []), f]);
		return ORDER.filter((t) => map.has(t)).map((t) => [t, map.get(t)!] as const);
	});

	const headline = $derived.by(() => {
		if (!data) return [];
		const r = data.reading;
		return [
			{ label: 'Titles', value: data.totals.titles.toLocaleString(), sub: 'with progress', icon: Layers, tint: '#a78bfa' },
			{ label: 'Finished', value: data.totals.finished.toLocaleString(), sub: 'shows, books, films…', icon: CheckCheck, tint: '#34d399' },
			{
				label: 'Watch time',
				value: data.totals.watch_minutes ? minutesLabel(data.totals.watch_minutes).replace(/ \d+m$/, '') : '—',
				sub: data.sections.tv ? `${data.sections.tv.units} episodes` : 'TV episodes',
				icon: Clock,
				tint: '#38bdf8'
			},
			{
				label: 'Pages read',
				value: r ? r.pages.toLocaleString() : '—',
				sub: r ? `${r.days_read} days · best streak ${r.longest_streak}` : 'no reading logged',
				icon: BookOpen,
				tint: '#fbbf24'
			}
		];
	});

	// Reading heatmap across the calendar year, weeks as columns.
	const weeks = $derived.by(() => {
		const days = data?.reading?.days ?? [];
		if (!days.length) return [];
		const first = new Date(days[0].date + 'T12:00:00');
		const pad = (first.getDay() + 6) % 7;
		const cells = [...Array(pad).fill(null), ...days];
		const out = [];
		for (let i = 0; i < cells.length; i += 7) out.push(cells.slice(i, i + 7));
		return out;
	});
	const peak = $derived(Math.max(1, ...(data?.reading?.days.map((d) => d.pages + d.chapters) ?? [1])));
	const level = (v: number) => (v === 0 ? 0 : Math.min(4, Math.ceil((v / peak) * 4)));
	const shades = ['rgb(255 255 255 / 0.05)', '#064e3b', '#047857', '#10b981', '#6ee7b7'];
</script>

<svelte:head><title>{year} in review · Encompass</title></svelte:head>

<header class="mb-8 flex flex-wrap items-end justify-between gap-4">
	<div>
		<p class="flex items-center gap-1.5 text-sm text-muted"><CalendarDays class="size-4" /> Year in review</p>
		<h1 class="mt-1 text-4xl font-semibold tracking-tight sm:text-5xl">{year}</h1>
		{#if data && year === thisYear}
			<p class="mt-2 text-sm text-faint">So far — updates as you go.</p>
		{/if}
	</div>
	<div class="flex items-center gap-1.5">
		<a
			href={older ? `/year/${older}` : undefined}
			aria-disabled={!older}
			class="grid size-9 place-items-center rounded-full border border-line text-muted transition hover:text-ink aria-disabled:pointer-events-none aria-disabled:opacity-30"
			aria-label="Previous year"
		>
			<ChevronLeft class="size-4" />
		</a>
		{#if years.length > 1}
			<select
				value={year}
				onchange={(e) => goto(`/year/${(e.target as HTMLSelectElement).value}`)}
				aria-label="Choose year"
				class="rounded-full border border-line bg-surface px-3 py-1.5 text-sm outline-none"
			>
				{#each years as y (y)}<option value={y}>{y}</option>{/each}
			</select>
		{/if}
		<a
			href={newer ? `/year/${newer}` : undefined}
			aria-disabled={!newer}
			class="grid size-9 place-items-center rounded-full border border-line text-muted transition hover:text-ink aria-disabled:pointer-events-none aria-disabled:opacity-30"
			aria-label="Next year"
		>
			<ChevronRight class="size-4" />
		</a>
	</div>
</header>

{#if error}
	<p class="text-muted">{error}</p>
{:else if !data}
	<div class="grid grid-cols-2 gap-3 lg:grid-cols-4">
		{#each [0, 1, 2, 3] as i (i)}<div class="h-28 animate-pulse rounded-2xl bg-ink/[0.03]"></div>{/each}
	</div>
	<div class="mt-8 h-64 animate-pulse rounded-2xl bg-ink/[0.03]"></div>
{:else if !sections.length && !data.reading?.pages}
	<div class="rounded-2xl border border-dashed border-line-strong p-10 text-center text-sm text-muted">
		Nothing tracked in {year}.
	</div>
{:else}
	<div class="grid grid-cols-2 gap-3 lg:grid-cols-4">
		{#each headline as card (card.label)}
			<div class="rounded-2xl border border-line bg-surface p-4">
				<div class="flex items-center gap-1.5 text-xs text-muted">
					<card.icon class="size-3.5" style="color: {card.tint}" />
					{card.label}
				</div>
				<p class="mt-2 text-3xl font-semibold tracking-tight tabular-nums">{card.value}</p>
				<p class="truncate text-xs text-faint">{card.sub}</p>
			</div>
		{/each}
	</div>

	<!-- Per media type: total, monthly bars, most-consumed titles -->
	<div class="mt-8 grid gap-4 md:grid-cols-2">
		{#each sections as section (section.type)}
			{@const meta = MEDIA[section.type]}
			<section class="rounded-2xl border border-line bg-surface p-5">
				<div class="flex items-start justify-between gap-3">
					<div>
						<p class="flex items-center gap-1.5 text-xs font-medium" style="color: {meta.accent}">
							<meta.icon class="size-3.5" />{meta.plural}
						</p>
						<p class="mt-1.5 text-2xl font-semibold tabular-nums">
							{section.units.toLocaleString()}
							<span class="text-sm font-normal text-muted">{unitWord(section.type, section.units)}</span>
						</p>
					</div>
					<div class="text-right text-xs text-faint">
						<p>{section.titles} {section.titles === 1 ? 'title' : 'titles'}</p>
						{#if section.finished}<p>{section.finished} finished</p>{/if}
						{#if section.minutes}<p>{minutesLabel(section.minutes)}</p>{/if}
					</div>
				</div>
				<MonthBars
					values={section.by_month}
					accent={meta.accent}
					unit={unitWord(section.type, 2)}
					label="{meta.plural} per month in {year}"
					class="mt-5"
				/>
				{#if section.top.length}
					<ol class="mt-5 space-y-2">
						{#each section.top.slice(0, 3) as top, i (top.id)}
							<li>
								<a href="/item/{top.media_type}/{top.id}" class="flex items-center gap-3 rounded-lg transition hover:bg-ink/[0.03]">
									<span class="w-3 text-xs text-faint tabular-nums">{i + 1}</span>
									<Cover src={top.item.image} title={top.item.title} type={top.media_type} class="aspect-[2/3] w-8 shrink-0 rounded" />
									<span class="min-w-0 flex-1 truncate text-sm">{top.item.title}</span>
									<span class="text-xs text-muted tabular-nums">{top.count.toLocaleString()} {unitWord(section.type, top.count)}</span>
								</a>
							</li>
						{/each}
					</ol>
				{/if}
			</section>
		{/each}
	</div>

	<!-- Reading calendar -->
	{#if data.reading && data.reading.days_read}
		{@const r = data.reading}
		<section class="mt-4 rounded-2xl border border-line bg-surface p-5">
			<div class="mb-4 flex flex-wrap items-center justify-between gap-2">
				<h2 class="flex items-center gap-1.5 text-[15px] font-semibold">
					<Flame class="size-4 text-orange-400" /> Reading days
				</h2>
				<p class="text-xs text-faint">
					{r.days_read} of {r.days_in_range} days · {minutesLabel(r.minutes)} · {r.sessions} sessions
					{#if r.best_day}· best day {formatDate(r.best_day.date)} ({r.best_day.pages} pages){/if}
				</p>
			</div>
			<div class="shelf overflow-x-auto">
				<div class="grid min-w-[40rem] gap-[3px]" style="grid-template-columns: repeat({weeks.length}, minmax(0, 1fr))">
					{#each weeks as week, w (w)}
						<div class="grid grid-rows-7 gap-[3px]">
							{#each Array(7) as _, d (d)}
								{@const day = week[d]}
								{#if day}
									<div
										class="aspect-square w-full rounded-[3px]"
										style="background: {shades[level(day.pages + day.chapters)]}"
										title="{formatDate(day.date)}: {day.pages} pages · {minutesLabel(day.minutes)}"
									></div>
								{:else}
									<div class="aspect-square w-full"></div>
								{/if}
							{/each}
						</div>
					{/each}
				</div>
			</div>
			<div class="mt-3 flex items-center justify-end gap-1 text-[10px] text-faint">
				Less {#each shades as s (s)}<span class="size-2.5 rounded-[2px]" style="background: {s}"></span>{/each} More
			</div>
		</section>
	{/if}

	<div class="mt-4 grid gap-4 lg:grid-cols-[1fr_18rem]">
		<!-- Top rated -->
		{#if data.top_rated.length}
			<section class="rounded-2xl border border-line bg-surface p-5">
				<h2 class="mb-4 flex items-center gap-1.5 text-[15px] font-semibold">
					<Star class="size-4 fill-amber-300 text-amber-300" /> Highest rated
				</h2>
				<div class="shelf -mx-1 flex gap-3 overflow-x-auto px-1 pb-1">
					{#each data.top_rated as entry (entry.media_type + entry.id)}
						<a href="/item/{entry.media_type}/{entry.id}" class="group w-24 shrink-0">
							<Cover src={entry.item.image} title={entry.item.title} type={entry.media_type} class="aspect-[2/3] w-full" />
							<p class="mt-1.5 line-clamp-1 text-xs font-medium">{entry.item.title}</p>
							<p class="text-[11px] text-faint">★ {entry.score}</p>
						</a>
					{/each}
				</div>
			</section>
		{/if}

		<!-- Genres -->
		{#if data.genres.length}
			{@const top = data.genres[0].count}
			<section class="rounded-2xl border border-line bg-surface p-5 {data.top_rated.length ? '' : 'lg:col-span-2'}">
				<h2 class="mb-4 text-[15px] font-semibold">Top genres</h2>
				<ol class="space-y-2">
					{#each data.genres.slice(0, 6) as genre (genre.name)}
						<li class="text-sm">
							<div class="flex justify-between"><span>{genre.name}</span><span class="text-xs text-faint tabular-nums">{genre.count}</span></div>
							<div class="mt-1 h-1.5 rounded-full bg-ink/8">
								<div class="h-full rounded-full bg-brand" style="width: {(genre.count / top) * 100}%"></div>
							</div>
						</li>
					{/each}
				</ol>
			</section>
		{/if}
	</div>

	<!-- Everything finished -->
	{#if finishedByType.length}
		<section class="mt-10">
			<h2 class="mb-4 text-xs font-semibold tracking-wider text-faint uppercase">Finished in {year}</h2>
			<div class="space-y-8">
				{#each finishedByType as [type, items] (type)}
					{@const meta = MEDIA[type]}
					<div>
						<p class="mb-3 flex items-center gap-1.5 text-sm font-medium">
							<meta.icon class="size-4" style="color: {meta.accent}" />{meta.plural}
							<span class="text-faint">{items.length}</span>
						</p>
						<div class="grid grid-cols-3 gap-x-3 gap-y-5 sm:grid-cols-5 lg:grid-cols-8">
							{#each items as entry (entry.id)}
								<a href="/item/{entry.media_type}/{entry.id}" class="group">
									<Cover src={entry.item.image} title={entry.item.title} type={entry.media_type} class="aspect-[2/3] w-full transition group-hover:ring-line-strong" />
									<p class="mt-1.5 line-clamp-1 text-xs font-medium">{entry.item.title}</p>
									<p class="text-[11px] text-faint">
										{formatDate(entry.end_date)}{entry.score ? ` · ★ ${entry.score}` : ''}
									</p>
								</a>
							{/each}
						</div>
					</div>
				{/each}
			</div>
		</section>
	{/if}
{/if}
