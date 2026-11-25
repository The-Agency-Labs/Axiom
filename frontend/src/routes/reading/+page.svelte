<script lang="ts">
	import { BookOpen, Clock, Flame, Smartphone, Trophy } from '@lucide/svelte';
	import { get, qs } from '#lib/api.ts';
	import Cover from '#lib/components/Cover.svelte';
	import ProgressBar from '#lib/components/ProgressBar.svelte';
	import { formatDate, MEDIA, minutesLabel } from '#lib/media.ts';
	import type { ReadingDocument, ReadingSession, ReadingStats } from '#lib/types.ts';

	const RANGE_DAYS = 364; // a year of weeks for the heatmap

	let stats = $state<ReadingStats | null>(null);
	let sessions = $state<ReadingSession[]>([]);
	let documents = $state<ReadingDocument[]>([]);
	let showAll = $state(false);

	function isoDaysAgo(n: number) {
		const d = new Date();
		d.setDate(d.getDate() - n);
		return d.toLocaleDateString('en-CA');
	}

	$effect(() => {
		const start = isoDaysAgo(RANGE_DAYS);
		Promise.all([
			get<ReadingStats>('/reading/stats' + qs({ start })),
			get<ReadingSession[]>('/reading/sessions' + qs({ start: isoDaysAgo(30), limit: 25 })),
			get<ReadingDocument[]>('/reading/documents')
		]).then(([s, sess, docs]) => {
			stats = s;
			sessions = sess;
			documents = docs;
		});
	});

	// Heatmap: columns are weeks (Mon–Sun), oldest on the left.
	const weeks = $derived.by(() => {
		if (!stats) return [];
		const days = stats.days;
		const first = new Date(days[0].date + 'T12:00:00');
		const pad = (first.getDay() + 6) % 7; // Monday = 0
		const cells: (ReadingStats['days'][number] | null)[] = [...Array(pad).fill(null), ...days];
		const out = [];
		for (let i = 0; i < cells.length; i += 7) out.push(cells.slice(i, i + 7));
		return out;
	});
	const peak = $derived(Math.max(1, ...(stats?.days.map((d) => d.pages + d.chapters) ?? [1])));
	const level = (v: number) => (v === 0 ? 0 : Math.min(4, Math.ceil((v / peak) * 4)));
	const shades = ['rgb(255 255 255 / 0.05)', '#064e3b', '#047857', '#10b981', '#6ee7b7'];

	const last30 = $derived(stats?.days.slice(-30) ?? []);
	const pages30 = $derived(last30.reduce((s, d) => s + d.pages, 0));
	const minutes30 = $derived(last30.reduce((s, d) => s + d.minutes, 0));
	const avgPages = $derived(last30.filter((d) => d.pages).length ? Math.round(pages30 / last30.filter((d) => d.pages).length) : 0);
</script>

<svelte:head><title>Reading · Encompass</title></svelte:head>

<header class="mb-8">
	<h1 class="text-3xl font-semibold tracking-tight">Reading</h1>
	<p class="mt-1 text-sm text-muted">Sessions from your Kindle and other readers.</p>
</header>

{#if !stats}
	<div class="h-40 animate-pulse rounded-2xl bg-white/[0.03]"></div>
{:else}
	<div class="grid grid-cols-2 gap-3 lg:grid-cols-4">
		{#each [
			{ label: 'Current streak', value: stats.current_streak, unit: 'days', icon: Flame, tint: '#fb923c' },
			{ label: 'Longest streak', value: stats.longest_streak, unit: 'days', icon: Trophy, tint: '#fbbf24' },
			{ label: 'Pages · 30 days', value: pages30, unit: `${avgPages}/day avg`, icon: BookOpen, tint: '#34d399' },
			{ label: 'Time · 30 days', value: minutesLabel(minutes30), unit: '', icon: Clock, tint: '#38bdf8' }
		] as card (card.label)}
			<div class="rounded-2xl border border-line bg-surface p-4">
				<div class="flex items-center gap-1.5 text-xs text-muted">
					<card.icon class="size-3.5" style="color: {card.tint}" />
					{card.label}
				</div>
				<p class="mt-2 text-3xl font-semibold tracking-tight tabular-nums">{card.value}</p>
				<p class="text-xs text-faint">{card.unit}</p>
			</div>
		{/each}
	</div>

	<section class="mt-8 rounded-2xl border border-line bg-surface p-5">
		<div class="mb-4 flex items-center justify-between">
			<h2 class="text-[15px] font-semibold">Past year</h2>
			<p class="text-xs text-faint">{stats.totals.days_read} of {stats.totals.days_in_range} days read</p>
		</div>
		<div class="grid gap-[3px]" style="grid-template-columns: repeat({weeks.length}, minmax(0, 1fr))">
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
		<div class="mt-3 flex items-center justify-end gap-1 text-[10px] text-faint">
			Less {#each shades as s (s)}<span class="size-2.5 rounded-[2px]" style="background: {s}"></span>{/each} More
		</div>
	</section>

	<div class="mt-8 grid gap-6 lg:grid-cols-2">
		<section>
			<h2 class="mb-3 text-[15px] font-semibold">Books on your devices</h2>
			{#if documents.length === 0}
				<p class="rounded-2xl border border-dashed border-line-strong p-6 text-sm text-muted">
					No books synced yet. Install the KOReader plugin to start.
				</p>
			{:else}
				<div class="space-y-2">
					{#each documents as doc (doc.id)}
						<div class="flex items-center gap-3 rounded-xl border border-line bg-surface p-3">
							<Cover src={doc.item?.image} title={doc.title} type="book" class="aspect-[2/3] w-11" />
							<div class="min-w-0 flex-1">
								<p class="truncate text-sm font-medium">{doc.item?.title ?? doc.title}</p>
								<p class="text-xs text-faint">
									{doc.pages_read} pages · {minutesLabel(doc.minutes)} · {doc.sessions} sessions
									{#if !doc.item}<span class="text-amber-300"> · not linked</span>{/if}
								</p>
								{#if doc.percentage !== null}
									<ProgressBar percent={doc.percentage * 100} accent={MEDIA.book.accent} class="mt-2" />
								{/if}
							</div>
							{#if doc.percentage !== null}
								<span class="text-xs text-muted tabular-nums">{Math.round(doc.percentage * 100)}%</span>
							{/if}
						</div>
					{/each}
				</div>
			{/if}
		</section>

		<section>
			<h2 class="mb-3 text-[15px] font-semibold">Recent sessions</h2>
			<ol class="divide-y divide-line overflow-hidden rounded-2xl border border-line bg-surface">
				{#each showAll ? sessions : sessions.slice(0, 8) as s (s.id)}
					<li class="flex items-center gap-3 px-4 py-3">
						<div class="w-12 shrink-0 text-center">
							<p class="text-[10px] text-faint uppercase">
								{new Date(s.started_at).toLocaleDateString(undefined, { weekday: 'short' })}
							</p>
							<p class="text-sm font-semibold tabular-nums">{new Date(s.started_at).getDate()}</p>
						</div>
						<div class="min-w-0 flex-1">
							<p class="truncate text-sm font-medium">{s.title}</p>
							<p class="flex items-center gap-1 text-xs text-faint">
								{new Date(s.started_at).toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' })}
								{#if s.device}· <Smartphone class="size-3" /> {s.device}{/if}
							</p>
						</div>
						<div class="text-right">
							<p class="text-sm font-semibold tabular-nums">{s.pages_read || s.chapters_read}<span class="ml-0.5 text-xs font-normal text-faint">{s.pages_read ? 'p' : 'ch'}</span></p>
							<p class="text-xs text-faint">{minutesLabel(s.minutes)}</p>
						</div>
					</li>
				{:else}
					<li class="px-4 py-6 text-sm text-muted">No sessions in the last 30 days.</li>
				{/each}
			</ol>
			{#if sessions.length > 8}
				<button
					onclick={() => (showAll = !showAll)}
					class="mt-2 w-full rounded-xl py-2 text-xs font-medium text-muted transition hover:bg-white/[0.03] hover:text-ink"
				>
					{showAll ? 'Show fewer' : `Show all ${sessions.length} sessions`}
				</button>
			{/if}
		</section>
	</div>
{/if}
