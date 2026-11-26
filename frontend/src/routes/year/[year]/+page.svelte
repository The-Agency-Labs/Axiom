<script lang="ts">
	import { BookOpen, CalendarDays, CheckCheck, Clock, Flame, Hourglass, Layers, Share2, Star } from '@lucide/svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { get } from '#lib/api.ts';
	import Cover from '#lib/components/Cover.svelte';
	import BarChart from '#lib/components/reading/BarChart.svelte';
	import { formatDate, MEDIA, minutesLabel } from '#lib/media.ts';
	import { toast } from '#lib/toast.svelte.ts';
	import type { MediaType, YearSnapshot } from '#lib/types.ts';
	import { Button, Card, Chip, ChipRow, EmptyState, Skeleton } from '#lib/ui/index.ts';

	const year = $derived(Number(page.params.year));
	let data = $state<YearSnapshot | null>(null);
	let years = $state<number[]>([]);
	let error = $state('');

	$effect(() => {
		const current = year;
		data = null;
		error = '';
		get<YearSnapshot>(`/years/${current}`)
			.then((s) => current === year && (data = s))
			.catch(() => (error = 'Could not build this year.'));
	});
	$effect(() => {
		get<number[]>('/years')
			.then((list) => (years = list))
			.catch(() => {});
	});

	const thisYear = new Date().getFullYear();
	// Year picker: every year with data, plus the current one.
	const picker = $derived([...new Set([...years, thisYear, year])].sort((a, b) => b - a));

	// Anime and anime movies are separate sections.
	const ORDER: MediaType[] = ['tv', 'movie', 'anime', 'anime_movie', 'manga', 'book', 'comic', 'game', 'boardgame'];
	const UNIT_WORDS: Partial<Record<MediaType, [string, string]>> = {
		tv: ['episode', 'episodes'],
		movie: ['movie', 'movies'],
		anime: ['episode', 'episodes'],
		anime_movie: ['movie', 'movies'],
		manga: ['chapter', 'chapters'],
		book: ['page', 'pages'],
		comic: ['issue', 'issues'],
		game: ['minute', 'minutes'],
		boardgame: ['play', 'plays']
	};
	const unitWord = (t: MediaType, n: number) => (UNIT_WORDS[t] ?? ['', ''])[n === 1 ? 0 : 1];
	const sections = $derived(ORDER.filter((t) => data?.sections[t]).map((t) => ({ type: t, ...data!.sections[t]! })));

	const finishedByType = $derived.by(() => {
		const map = new Map<MediaType, YearSnapshot['finished']>();
		for (const f of data?.finished ?? []) map.set(f.media_type, [...(map.get(f.media_type) ?? []), f]);
		return ORDER.filter((t) => map.has(t)).map((t) => [t, map.get(t)!] as const);
	});

	const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
	const monthBars = (values: number[]) =>
		values.map((value, i) => ({ tick: MONTHS[i][0], label: `${MONTHS[i]} ${year}`, value }));

	const headline = $derived.by(() => {
		if (!data) return [];
		const r = data.reading;
		return [
			{ label: 'Titles', value: data.totals.titles.toLocaleString(), sub: 'with progress', icon: Layers },
			{ label: 'Finished', value: data.totals.finished.toLocaleString(), sub: 'all types', icon: CheckCheck },
			{
				label: 'Watch time',
				value: data.totals.watch_minutes ? minutesLabel(data.totals.watch_minutes).replace(/ \d+m$/, '') : '-',
				sub: 'screen time',
				icon: Clock
			},
			{
				label: 'Time reading',
				value: r?.minutes ? minutesLabel(r.minutes).replace(/ \d+m$/, '') : '-',
				sub: r?.minutes ? `${minutesLabel(r.minutes)} · ${r.sessions} session${r.sessions === 1 ? '' : 's'}` : 'no reading time logged',
				icon: Hourglass
			},
			{
				label: 'Pages read',
				value: r ? r.pages.toLocaleString() : '-',
				sub: r ? `${r.days_read} days · best streak ${r.longest_streak}` : 'no reading logged',
				icon: BookOpen
			}
		];
	});

	// Reading heatmap across the year, weeks as columns.
	const weeks = $derived.by(() => {
		const days = data?.reading?.days ?? [];
		if (!days.length) return [];
		const first = new Date(days[0].date + 'T12:00:00');
		const cells = [...Array((first.getDay() + 6) % 7).fill(null), ...days];
		const out = [];
		for (let i = 0; i < cells.length; i += 7) out.push(cells.slice(i, i + 7));
		return out;
	});
	const peak = $derived(Math.max(1, ...(data?.reading?.days.map((d) => d.pages + d.chapters) ?? [1])));
	const level = (v: number) => (v === 0 ? 0 : Math.min(4, Math.ceil((v / peak) * 4)));
	const shades = ['var(--color-ink)', 'var(--color-type-book)', 'var(--color-type-book)', 'var(--color-type-book)', 'var(--color-type-book)'];
	const opacity = [0.07, 0.3, 0.5, 0.75, 1];

	async function share() {
		const url = location.href;
		try {
			if (navigator.share) await navigator.share({ title: `My ${year} in review`, url });
			else {
				await navigator.clipboard.writeText(url);
				toast('Link copied', { kind: 'success' });
			}
		} catch {
			/* cancelled */
		}
	}
</script>

<svelte:head><title>{year} in review · Axiom</title></svelte:head>

<div class="mx-auto max-w-4xl">
	<header class="mb-4 flex items-end justify-between gap-3">
		<div>
			<p class="flex items-center gap-1.5 text-sm text-muted"><CalendarDays class="size-4" />Year in review</p>
			<h1 class="mt-1 text-5xl font-semibold tracking-tight tabular-nums">{year}</h1>
			{#if year === thisYear}<p class="mt-1 text-sm text-faint">So far, updating as you go.</p>{/if}
		</div>
		<Button variant="secondary" icon={Share2} onclick={share} class="print:hidden">Share</Button>
	</header>

	<ChipRow label="Choose year" sticky={false}>
		{#each picker as y (y)}
			<Chip selected={y === year} onclick={() => goto(`/year/${y}`, { replaceState: true })}>{y}</Chip>
		{/each}
	</ChipRow>

	{#if error}
		<EmptyState icon={CalendarDays} title="Could not build this year" description={error} />
	{:else if !data}
		<div class="mt-3 grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-5" aria-hidden="true">
			{#each [0, 1, 2, 3, 4] as i (i)}<Skeleton class="h-28 w-full" />{/each}
		</div>
		<Skeleton class="mt-6 h-64 w-full" />
	{:else if !sections.length && !data.reading?.pages}
		<EmptyState icon={CalendarDays} title="Nothing tracked in {year}" description="Finish or progress something and it will show up here." />
	{:else}
		<div class="mt-3 grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-5">
			{#each headline as card (card.label)}
				<div class="rounded-[var(--radius-card)] border border-line bg-surface p-4">
					<p class="flex items-center gap-1.5 text-xs text-muted"><card.icon class="size-3.5" />{card.label}</p>
					<p class="mt-2 text-3xl font-semibold tracking-tight tabular-nums">{card.value}</p>
					<p class="truncate text-xs text-faint">{card.sub}</p>
				</div>
			{/each}
		</div>

		<div class="mt-6 grid gap-4 md:grid-cols-2">
			{#each sections as section (section.type)}
				{@const meta = MEDIA[section.type]}
				<section class="min-w-0 rounded-[var(--radius-card)] border border-line bg-surface p-4">
					<div class="flex items-start justify-between gap-3">
						<div>
							<p class="flex items-center gap-1.5 text-xs font-medium" style="color: {meta.accent}"><meta.icon class="size-3.5" />{meta.plural}</p>
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
					{#if section.by_month.some((v) => v > 0)}
					<div class="mt-5">
						<BarChart
							bars={monthBars(section.by_month)}
							accent={meta.accent}
							unit={unitWord(section.type, 2)}
							title="{meta.plural}: {unitWord(section.type, 2)} per month in {year}"
							height={96}
						/>
					</div>
					{/if}
					{#if section.top.length}
						<ol class="mt-5 space-y-1">
							{#each section.top.slice(0, 3) as top, i (top.id)}
								<li>
									<a href="/item/{top.media_type}/{top.id}" class="flex min-h-11 items-center gap-3 rounded-lg hover:bg-ink/5">
										<span class="w-3 text-xs text-faint tabular-nums">{i + 1}</span>
										<Cover src={top.item.image} title={top.item.title} type={top.media_type} class="aspect-[2/3] w-8 shrink-0 !rounded" />
										<span class="min-w-0 flex-1 truncate text-sm">{top.item.title}</span>
										<span class="text-xs text-muted tabular-nums">{top.count.toLocaleString()}</span>
									</a>
								</li>
							{/each}
						</ol>
					{/if}
				</section>
			{/each}
		</div>

		{#if data.reading && data.reading.days_read}
			{@const r = data.reading}
			<Card class="mt-4" title="Reading days" icon={Flame} accent="var(--color-type-book)"
				description="{r.days_read} of {r.days_in_range} days · {minutesLabel(r.minutes)}{r.best_day ? ` · best day ${formatDate(r.best_day.date)} (${r.best_day.pages} pages)` : ''}">
				<div class="shelf overflow-x-auto pb-1">
					<div class="grid min-w-[36rem] gap-[3px]" style="grid-template-columns: repeat({weeks.length}, minmax(0, 1fr))" role="img" aria-label="{r.days_read} reading days in {year}">
						{#each weeks as week, w (w)}
							<div class="grid grid-rows-7 gap-[3px]">
								{#each Array(7) as _, d (d)}
									{@const day = week[d]}
									{#if day}
										{@const l = level(day.pages + day.chapters)}
										<div
											class="aspect-square w-full rounded-[3px]"
											style="background: {shades[l]}; opacity: {opacity[l]}"
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
				<div class="mt-3">
					<BarChart
						bars={monthBars(r.pages_by_month)}
						accent="var(--color-type-book)"
						unit="pages"
						title="Pages read per month in {year}"
						height={96}
					/>
				</div>
			</Card>
		{/if}

		<div class="mt-4 grid gap-4 md:grid-cols-[minmax(0,1fr)_minmax(0,18rem)]">
			{#if data.top_rated.length}
				<Card title="Highest rated" icon={Star} accent="var(--color-warning)" class="min-w-0">
					<div class="shelf -mx-4 flex gap-3 overflow-x-auto px-4 pb-1 scroll-px-4">
						{#each data.top_rated as entry (entry.media_type + entry.id)}
							<a href="/item/{entry.media_type}/{entry.id}" class="w-24 shrink-0">
								<Cover src={entry.item.image} title={entry.item.title} type={entry.media_type} class="aspect-[2/3] w-full" />
								<p class="mt-1.5 line-clamp-1 text-xs font-medium">{entry.item.title}</p>
								<p class="text-[0.75rem] text-faint">★ {entry.score}</p>
							</a>
						{/each}
					</div>
				</Card>
			{/if}
			{#if data.genres.length}
				{@const top = data.genres[0].count}
				<Card title="Top genres" class="min-w-0 {data.top_rated.length ? '' : 'md:col-span-2'}">
					<ol class="space-y-2.5">
						{#each data.genres.slice(0, 6) as genre (genre.name)}
							<li class="text-sm">
								<div class="flex justify-between"><span>{genre.name}</span><span class="text-xs text-faint tabular-nums">{genre.count}</span></div>
								<div class="mt-1 h-1.5 rounded-full bg-ink/8"><div class="h-full rounded-full bg-brand" style="width: {(genre.count / top) * 100}%"></div></div>
							</li>
						{/each}
					</ol>
				</Card>
			{/if}
		</div>

		{#if finishedByType.length}
			<section class="mt-10">
				<h2 class="mb-4 text-[0.8125rem] font-semibold tracking-wide text-faint uppercase">Finished in {year}</h2>
				<div class="space-y-8">
					{#each finishedByType as [type, items] (type)}
						{@const meta = MEDIA[type]}
						<div>
							<p class="mb-3 flex items-center gap-1.5 text-sm font-medium">
								<meta.icon class="size-4" style="color: {meta.accent}" />{meta.plural}<span class="text-faint">{items.length}</span>
							</p>
							<div class="grid grid-cols-3 gap-x-3 gap-y-5 sm:grid-cols-5 lg:grid-cols-6">
								{#each items as entry (entry.id)}
									<a href="/item/{entry.media_type}/{entry.id}">
										<Cover src={entry.item.image} title={entry.item.title} type={entry.media_type} class="aspect-[2/3] w-full" />
										<p class="mt-1.5 line-clamp-1 text-xs font-medium">{entry.item.title}</p>
										<p class="text-[0.75rem] text-faint">{formatDate(entry.end_date)}{entry.score ? ` · ★ ${entry.score}` : ''}</p>
									</a>
								{/each}
							</div>
						</div>
					{/each}
				</div>
			</section>
		{/if}
	{/if}
</div>
