<script lang="ts">
	import { BookOpen, Clock, Flame } from '@lucide/svelte';
	import { minutesLabel } from '#lib/media.ts';
	import type { ReadingStats } from '#lib/types.ts';
	import { isoDaysAgo, parseDay } from './format.ts';

	let { stats }: { stats: ReadingStats } = $props();

	const today = $derived(stats.today);
	const week = $derived(stats.days.slice(-7));
	const weekMax = $derived(Math.max(1, ...week.map((d) => d.pages + d.chapters)));
	const todayIso = isoDaysAgo(0);
	// Tapping a day of the week shows its numbers; tapping it again returns to today.
	let picked = $state<string | null>(null);
	const shown = $derived(week.find((d) => d.date === picked) ?? today);
	const dayName = $derived(
		!picked || picked === todayIso
			? 'Today'
			: parseDay(picked).toLocaleDateString(undefined, { weekday: 'long', month: 'short', day: 'numeric' })
	);
</script>

<section class="rounded-[var(--radius-card)] border border-line bg-surface p-4" aria-label="This week">
	<h2 class="mb-2 text-[0.8125rem] font-semibold" aria-live="polite">{dayName}</h2>
	<div class="grid grid-cols-3 gap-2">
		<div>
			<p class="flex items-center gap-1.5 text-[0.75rem] text-muted"><BookOpen class="size-3.5 text-type-book" />Pages</p>
			<p class="mt-1 text-3xl font-semibold tracking-tight tabular-nums">{shown?.pages ?? 0}</p>
		</div>
		<div>
			<p class="flex items-center gap-1.5 text-[0.75rem] text-muted"><Clock class="size-3.5 text-type-book" />Time</p>
			<p class="mt-1 text-3xl font-semibold tracking-tight tabular-nums">{minutesLabel(shown?.minutes ?? 0)}</p>
		</div>
		<div>
			<p class="flex items-center gap-1.5 text-[0.75rem] text-muted"><Flame class="size-3.5 text-warning" />Streak</p>
			<p class="mt-1 text-3xl font-semibold tracking-tight tabular-nums">
				{stats.current_streak}<span class="ml-1 text-sm font-normal text-faint">{stats.current_streak === 1 ? 'day' : 'days'}</span>
			</p>
		</div>
	</div>
	{#if !stats.read_today && dayName === 'Today'}
		<p class="mt-2 text-[0.8125rem] text-muted">
			{stats.current_streak ? 'Read a little today to keep your streak.' : 'Nothing read yet today.'}
		</p>
	{/if}

	<ol class="mt-3 grid grid-cols-7 gap-1" aria-label="Last 7 days">
		{#each week as d (d.date)}
			{@const v = d.pages + d.chapters}
			{@const on = d.date === (picked ?? todayIso)}
			<li>
				<button
					type="button"
					class="flex w-full flex-col items-center gap-1 rounded-lg py-1 transition {on ? 'bg-ink/8' : 'hover:bg-ink/5'}"
					aria-pressed={on}
					aria-label="{parseDay(d.date).toLocaleDateString(undefined, { weekday: 'long' })}: {d.pages} pages, {minutesLabel(d.minutes)}"
					onclick={() => (picked = picked === d.date ? null : d.date)}
				>
					<span class="flex h-10 w-full items-end justify-center">
						<span
							class="block w-3 rounded-t-[4px] {v ? 'bg-type-book' : 'bg-ink/10'}"
							style="height: {v ? Math.max(12, (v / weekMax) * 100) : 6}%"
						></span>
					</span>
					<span
						class="grid size-6 place-items-center rounded-full text-[0.75rem] {d.date === todayIso
							? 'bg-ink text-canvas font-semibold'
							: on
								? 'font-semibold text-ink'
								: 'text-faint'}"
						>{parseDay(d.date).toLocaleDateString(undefined, { weekday: 'narrow' })}</span
					>
				</button>
			</li>
		{/each}
	</ol>
</section>
