<script lang="ts">
	import { get, qs } from '#lib/api.ts';
	import { minutesLabel } from '#lib/media.ts';
	import { Card, Skeleton, Tabs } from '#lib/ui/index.ts';
	import type { ReadingStats } from '#lib/types.ts';
	import BarChart from './BarChart.svelte';
	import type { Bar } from './types.ts';
	import { isoDaysAgo, parseDay } from './format.ts';

	let range = $state('30');
	let metric = $state('pages');
	let stats = $state<ReadingStats | null>(null);

	$effect(() => {
		const r = range;
		stats = null;
		get<ReadingStats>('/reading/stats' + qs({ start: isoDaysAgo(Number(r) - 1) })).then((s) => {
			if (r === range) stats = s;
		});
	});

	const value = (d: { pages: number; minutes: number }) => (metric === 'pages' ? d.pages : d.minutes);

	const bars = $derived.by<Bar[]>(() => {
		if (!stats) return [];
		const days = stats.days;
		if (Number(range) <= 30) {
			return days.map((d, i) => {
				const dt = parseDay(d.date);
				return {
					tick: range === '7' ? dt.toLocaleDateString(undefined, { weekday: 'short' }) : dt.getDate() % 5 === 0 || i === 0 ? String(dt.getDate()) : '',
					label: dt.toLocaleDateString(undefined, { weekday: 'short', month: 'short', day: 'numeric' }),
					value: value(d)
				};
			});
		}
		// Longer ranges: weekly (90 days) or monthly (a year) buckets.
		const out = new Map<string, Bar>();
		for (const d of days) {
			const dt = parseDay(d.date);
			let key: string, tick: string, label: string;
			if (range === '90') {
				const monday = new Date(dt);
				monday.setDate(dt.getDate() - ((dt.getDay() + 6) % 7));
				key = monday.toISOString().slice(0, 10);
				tick = monday.getDate() <= 7 ? monday.toLocaleDateString(undefined, { month: 'short' }) : '';
				label = 'Week of ' + monday.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
			} else {
				key = d.date.slice(0, 7);
				tick = dt.toLocaleDateString(undefined, { month: 'narrow' });
				label = dt.toLocaleDateString(undefined, { month: 'long', year: 'numeric' });
			}
			const b = out.get(key) ?? { tick, label, value: 0 };
			b.value += value(d);
			out.set(key, b);
		}
		return [...out.values()];
	});
</script>

<Card title="Reading stats" description="Daily pages and time">
	<div class="flex flex-wrap gap-2">
		<Tabs
			label="Range"
			bind:value={range}
			options={[
				{ value: '7', label: '7d' },
				{ value: '30', label: '30d' },
				{ value: '90', label: '90d' },
				{ value: '365', label: '1y' }
			]}
		/>
		<Tabs
			label="Metric"
			bind:value={metric}
			options={[
				{ value: 'pages', label: 'Pages' },
				{ value: 'minutes', label: 'Minutes' }
			]}
		/>
	</div>
	{#if !stats}
		<Skeleton class="mt-4 h-40 w-full" />
	{:else}
		<div class="mt-5">
			<BarChart
				{bars}
				unit={metric === 'pages' ? 'pages' : 'min'}
				title="{metric === 'pages' ? 'Pages' : 'Minutes'} read per {range === '365' ? 'month' : range === '90' ? 'week' : 'day'}"
				tickEvery={1}
			/>
		</div>
		<dl class="mt-5 grid grid-cols-3 gap-2 text-center">
			<div class="rounded-xl bg-raised py-2.5">
				<dd class="text-lg font-semibold tabular-nums">{stats.totals.pages.toLocaleString()}</dd>
				<dt class="text-[11px] text-faint">pages</dt>
			</div>
			<div class="rounded-xl bg-raised py-2.5">
				<dd class="text-lg font-semibold tabular-nums">{minutesLabel(stats.totals.minutes)}</dd>
				<dt class="text-[11px] text-faint">time</dt>
			</div>
			<div class="rounded-xl bg-raised py-2.5">
				<dd class="text-lg font-semibold tabular-nums">{stats.totals.days_read}<span class="text-xs font-normal text-faint">/{stats.totals.days_in_range}</span></dd>
				<dt class="text-[11px] text-faint">days read</dt>
			</div>
		</dl>
		<p class="mt-3 text-center text-[12px] text-faint">Longest streak {stats.longest_streak} days</p>
	{/if}
</Card>
