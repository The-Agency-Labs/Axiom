<script lang="ts">
	import { BookOpen, CalendarCheck, Clock, Flame } from '@lucide/svelte';
	import { get, qs } from '#lib/api.ts';
	import { minutesLabel } from '#lib/media.ts';
	import { Skeleton, Tabs } from '#lib/ui/index.ts';
	import type { ReadingStats } from '#lib/types.ts';
	import BarChart from './BarChart.svelte';
	import type { Bar } from './types.ts';
	import { isoDate, isoDaysAgo, parseDay } from './format.ts';

	let range = $state('30');
	let metric = $state('pages');
	let stats = $state<ReadingStats | null>(null);

	/** The first day of a range; a year is this month and the eleven before it. */
	function startOf(r: string) {
		if (r !== '365') return isoDaysAgo(Number(r) - 1);
		const now = new Date();
		return isoDate(new Date(now.getFullYear(), now.getMonth() - 11, 1));
	}

	$effect(() => {
		const r = range;
		stats = null;
		get<ReadingStats>('/reading/stats' + qs({ start: startOf(r) })).then((s) => {
			if (r === range) stats = s;
		});
	});

	const per = $derived(range === '365' ? 'month' : range === '90' ? 'week' : 'day');
	const bars = $derived.by<Bar[]>(() => {
		if (!stats) return [];
		const out = new Map<string, Bar>();
		const days = stats.days;
		days.forEach((d, i) => {
			const dt = parseDay(d.date);
			const short: Intl.DateTimeFormatOptions = { month: 'short', day: 'numeric' };
			let key = d.date;
			let tick = '';
			let label = dt.toLocaleDateString(undefined, { weekday: 'short', ...short });
			if (per === 'day') {
				// A week of weekdays, or a date every seven days clear of both edges.
				if (days.length <= 7) tick = dt.toLocaleDateString(undefined, { weekday: 'short' });
				else if ((days.length - 1 - i) % 7 === 3) tick = dt.toLocaleDateString(undefined, short);
			} else if (per === 'week') {
				const monday = new Date(dt);
				monday.setDate(dt.getDate() - ((dt.getDay() + 6) % 7));
				key = isoDate(monday);
				tick = monday.getDate() <= 7 ? monday.toLocaleDateString(undefined, { month: 'short' }) : '';
				label = 'Week of ' + monday.toLocaleDateString(undefined, short);
			} else {
				key = d.date.slice(0, 7);
				// Every other month, ending on the current one.
				const now = new Date();
				const ago = (now.getFullYear() - dt.getFullYear()) * 12 + now.getMonth() - dt.getMonth();
				tick = ago % 2 === 0 ? dt.toLocaleDateString(undefined, { month: 'short' }) : '';
				label = dt.toLocaleDateString(undefined, { month: 'long', year: 'numeric' });
			}
			const bar = out.get(key) ?? { tick, label, value: 0 };
			bar.value += metric === 'pages' ? d.pages : d.minutes;
			out.set(key, bar);
		});
		return [...out.values()];
	});

	const tiles = $derived.by(() => {
		if (!stats) return [];
		const t = stats.totals;
		const each = (n: number) => Math.round(n / Math.max(1, t.days_read));
		return [
			{ label: 'Pages', icon: BookOpen, value: t.pages.toLocaleString(), sub: `${each(t.pages)} per reading day` },
			{ label: 'Time', icon: Clock, value: minutesLabel(t.minutes), sub: `${minutesLabel(each(t.minutes))} per reading day` },
			{ label: 'Days read', icon: CalendarCheck, value: String(t.days_read), sub: `of ${t.days_in_range} days` },
			{ label: 'Streak', icon: Flame, value: `${stats.current_streak}d`, sub: `best ever ${stats.longest_streak} days` }
		];
	});
</script>

<section class="space-y-3" aria-label="Reading statistics">
	<Tabs
		label="Range"
		full
		bind:value={range}
		options={[
			{ value: '7', label: '7 days' },
			{ value: '30', label: '30 days' },
			{ value: '90', label: '90 days' },
			{ value: '365', label: '12 months' }
		]}
	/>

	{#if !stats}
		<Skeleton class="h-52 w-full" />
		<Skeleton class="h-52 w-full" />
	{:else}
		<dl class="grid grid-cols-2 gap-3 xl:grid-cols-4">
			{#each tiles as tile (tile.label)}
				<div class="rounded-[var(--radius-card)] border border-line bg-surface p-4">
					<dt class="flex items-center gap-1.5 text-[0.75rem] text-muted"><tile.icon class="size-3.5 text-type-book" />{tile.label}</dt>
					<dd class="mt-1 text-2xl font-semibold tracking-tight tabular-nums">{tile.value}</dd>
					<dd class="text-[0.75rem] text-faint">{tile.sub}</dd>
				</div>
			{/each}
		</dl>

		<div class="rounded-[var(--radius-card)] border border-line bg-surface p-4">
			<div class="mb-4 flex flex-wrap items-center justify-between gap-3">
				<h2 class="text-[0.9375rem] font-semibold">{metric === 'pages' ? 'Pages' : 'Minutes'} per {per}</h2>
				<Tabs
					label="Metric"
					bind:value={metric}
					options={[
						{ value: 'pages', label: 'Pages' },
						{ value: 'minutes', label: 'Minutes' }
					]}
				/>
			</div>
			<BarChart {bars} unit={metric === 'pages' ? 'pages' : 'min'} title="{metric === 'pages' ? 'Pages' : 'Minutes'} read per {per}" height={160} />
		</div>
		<p class="px-1 text-[0.75rem] text-faint">From your Kindle. A page opened on its own, with nothing read around it, isn't counted.</p>
	{/if}
</section>
