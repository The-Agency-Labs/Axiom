<script lang="ts">
	import { CalendarDays, CalendarPlus, ChevronLeft, ChevronRight } from '@lucide/svelte';
	import { get, qs } from '#lib/api.ts';
	import Cover from '#lib/components/Cover.svelte';
	import { isoDate, parseDay } from '#lib/components/reading/format.ts';
	import type { CalendarEvent } from '#lib/components/reading/types.ts';
	import { MEDIA, LIBRARY_TYPES, titleHref } from '#lib/media.ts';
	import { loadSettings, settings } from '#lib/settings.svelte.ts';
	import type { MediaType } from '#lib/types.ts';
	import { Button, Chip, ChipRow, CopyField, EmptyState, IconButton, PullToRefresh, Sheet, Skeleton, Tabs } from '#lib/ui/index.ts';

	let view = $state('agenda');
	let events = $state<CalendarEvent[] | null>(null);
	let hidden = $state<Set<MediaType>>(new Set());
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

	async function load() {
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
		events = null;
		load();
	});
	$effect(() => {
		loadSettings();
	});

	const types = $derived([...new Set((events ?? []).map((e) => e.media_type))].sort((a, b) => LIBRARY_TYPES.indexOf(a) - LIBRARY_TYPES.indexOf(b)));
	const shown = $derived((events ?? []).filter((e) => !hidden.has(e.media_type)));
	const byDay = $derived.by(() => {
		const map = new Map<string, CalendarEvent[]>();
		for (const e of shown) map.set(e.date, [...(map.get(e.date) ?? []), e]);
		return [...map.entries()].sort(([a], [b]) => a.localeCompare(b));
	});

	function toggle(t: MediaType) {
		const next = new Set(hidden);
		if (next.has(t)) next.delete(t);
		else next.add(t);
		hidden = next;
	}

	const heading = (iso: string) => {
		if (iso === today) return 'Today';
		const d = parseDay(iso);
		const t = parseDay(today);
		if (Math.round((d.getTime() - t.getTime()) / 864e5) === 1) return 'Tomorrow';
		return d.toLocaleDateString(undefined, { weekday: 'long', month: 'short', day: 'numeric' });
	};
	const timeOf = (e: CalendarEvent) =>
		e.all_day || !e.time ? '' : new Date(e.time).toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' });

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

{#snippet eventRow(e: CalendarEvent)}
	{@const meta = MEDIA[e.media_type]}
	<li>
	<a href={titleHref(e)} class="flex min-h-14 items-center gap-3 px-3 py-2 active:bg-ink/5">
		<Cover src={e.image} title={e.title} type={e.media_type} class="aspect-[2/3] w-9 shrink-0 !rounded-md" />
		<div class="min-w-0 flex-1">
			<p class="line-clamp-2 text-[0.875rem] leading-snug font-medium">{e.title}</p>
			<p class="flex items-center gap-1 text-[0.75rem] text-faint"><meta.icon class="size-3" style="color: {meta.accent}" />{meta.label}{timeOf(e) ? ` · ${timeOf(e)}` : ''}</p>
		</div>
	</a>
	</li>
{/snippet}

<svelte:head><title>Calendar · Axiom</title></svelte:head>

<PullToRefresh onrefresh={load}>
	<header class="mb-3 flex items-center justify-between gap-3">
		<h1 class="text-3xl font-semibold tracking-tight">Calendar</h1>
		<Button variant="secondary" icon={CalendarPlus} onclick={() => (subscribeOpen = true)}>Subscribe</Button>
	</header>

	<Tabs label="Calendar view" bind:value={view} options={[{ value: 'agenda', label: 'Agenda' }, { value: 'month', label: 'Month' }]} />

	{#if types.length > 1}
		<ChipRow label="Filter by type">
			{#each types as t (t)}
				{@const meta = MEDIA[t]}
				<Chip selected={!hidden.has(t)} color={meta.accent} onclick={() => toggle(t)}>{meta.plural}</Chip>
			{/each}
		</ChipRow>
	{/if}

	{#if events === null}
		<div class="mt-4 space-y-3" aria-hidden="true"><Skeleton class="h-6 w-40" /><Skeleton class="h-40 w-full" /></div>
	{:else if view === 'agenda'}
		<div class="mt-3">
			{#if !showPast}
				<Button variant="ghost" size="sm" onclick={() => (showPast = true)} class="mb-2">Show the last 30 days</Button>
			{/if}
			{#if byDay.length === 0}
				<EmptyState icon={CalendarDays} title="Nothing coming up" description="Upcoming episodes and releases for what you track will show up here." />
			{:else}
				<div class="space-y-5">
					{#each byDay as [iso, list] (iso)}
						<section aria-label={heading(iso)}>
							<h2 class="mb-1.5 px-1 text-[0.875rem] font-semibold {iso === today ? 'text-brand' : ''} {iso < today ? 'text-muted' : ''}">{heading(iso)}</h2>
							<ul class="divide-y divide-line overflow-hidden rounded-[var(--radius-card)] border border-line bg-surface">
								{#each list as e, i (e.item_id + ':' + e.season + ':' + e.episode + ':' + i)}{@render eventRow(e)}{/each}
							</ul>
						</section>
					{/each}
				</div>
			{/if}
		</div>
	{:else}
		<div class="mt-3">
			<div class="mb-2 flex items-center justify-between">
				<IconButton icon={ChevronLeft} label="Previous month" onclick={() => shift(-1)} />
				<h2 class="text-[1rem] font-semibold">{month.toLocaleDateString(undefined, { month: 'long', year: 'numeric' })}</h2>
				<IconButton icon={ChevronRight} label="Next month" onclick={() => shift(1)} />
			</div>
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
						<ul class="divide-y divide-line overflow-hidden rounded-[var(--radius-card)] border border-line bg-surface">
							{#each list as e, i (i)}{@render eventRow(e)}{/each}
						</ul>
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
