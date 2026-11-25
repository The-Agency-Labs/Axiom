<script lang="ts">
	import { BookOpen, Clock, Flame } from '@lucide/svelte';
	import { minutesLabel } from '#lib/media.ts';
	import type { ReadingStats } from '#lib/types.ts';

	let { stats }: { stats: ReadingStats } = $props();

	const week = $derived(stats.days.slice(-7));
	const peak = $derived(Math.max(1, ...week.map((d) => d.pages + d.chapters)));
	const weekPages = $derived(week.reduce((sum, d) => sum + d.pages, 0));
	const weekday = (iso: string) =>
		new Date(iso + 'T12:00:00').toLocaleDateString(undefined, { weekday: 'narrow' });
</script>

<a
	href="/reading"
	class="grid gap-5 rounded-2xl border border-line bg-surface p-5 transition hover:border-line-strong sm:grid-cols-[1fr_auto] sm:items-end"
>
	<div class="grid grid-cols-3 gap-4">
		<div>
			<div class="flex items-center gap-1.5 text-xs text-muted">
				<Flame class="size-3.5 {stats.current_streak ? 'text-orange-400' : ''}" /> Streak
			</div>
			<p class="mt-1 text-2xl font-semibold tabular-nums">
				{stats.current_streak}<span class="ml-1 text-sm font-normal text-faint">days</span>
			</p>
		</div>
		<div>
			<div class="flex items-center gap-1.5 text-xs text-muted">
				<BookOpen class="size-3.5" /> Today
			</div>
			<p class="mt-1 text-2xl font-semibold tabular-nums">
				{stats.today?.pages ?? 0}<span class="ml-1 text-sm font-normal text-faint">pages</span>
			</p>
		</div>
		<div>
			<div class="flex items-center gap-1.5 text-xs text-muted">
				<Clock class="size-3.5" /> This week
			</div>
			<p class="mt-1 text-2xl font-semibold tabular-nums">
				{weekPages}<span class="ml-1 text-sm font-normal text-faint">pages</span>
			</p>
		</div>
	</div>

	<div class="flex h-14 items-end gap-1.5" aria-label="Pages read in the last 7 days">
		{#each week as day (day.date)}
			{@const value = day.pages + day.chapters}
			<div class="flex flex-col items-center gap-1" title="{day.date}: {day.pages} pages, {minutesLabel(day.minutes)}">
				<div class="flex h-10 w-6 items-end overflow-hidden rounded-md bg-ink/5">
					<div
						class="w-full rounded-md bg-emerald-400/85"
						style="height: {value ? Math.max(12, (value / peak) * 100) : 0}%"
					></div>
				</div>
				<span class="text-[10px] text-faint">{weekday(day.date)}</span>
			</div>
		{/each}
	</div>
</a>
