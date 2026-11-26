<script lang="ts">
	import { Check, CheckCheck, RotateCcw, Sparkles } from '@lucide/svelte';
	import { minutesLabel } from '#lib/media.ts';
	import { Button, EmptyState, ProgressBar, Skeleton } from '#lib/ui/index.ts';
	import EpisodeItem from './EpisodeItem.svelte';
	import type { ShowState } from './show.svelte.ts';

	/** Season tabs + episode list for a show or anime. Specials (season 0) come last. */
	let { state: s, accent }: { state: ShowState; accent: string } = $props();

	const season = $derived(s.current);
	const allWatched = $derived(!!season && season.total > 0 && season.watched >= season.total);
	const regular = $derived(s.ordered.filter((x) => !x.is_special));
	const nextEp = $derived(s.nextUp);
</script>

{#if s.error}
	<EmptyState title={s.error} description="Episode details come from TheTVDB; try again in a moment." />
{:else if !s.show}
	<Skeleton class="h-20 w-full" />
	<Skeleton class="mt-3 h-11 w-full" />
	<div class="mt-3 space-y-2">
		{#each [0, 1, 2] as i (i)}<Skeleton class="h-16 w-full" />{/each}
	</div>
{:else}
	<section class="rounded-[var(--radius-card)] border border-line bg-surface p-4">
		<div class="flex flex-wrap items-end justify-between gap-x-4 gap-y-1">
			<p class="text-2xl font-semibold tabular-nums">
				{s.show.completion.percent ?? 0}%
				<span class="ml-1 text-sm font-normal text-muted">{s.show.completion.watched} / {s.show.completion.total} episodes</span>
			</p>
			<p class="text-xs text-faint">
				{s.show.completion.seasons_completed} of {s.show.completion.seasons} seasons
				{#if s.show.completion.specials_watched}· {s.show.completion.specials_watched} special{s.show.completion.specials_watched === 1 ? '' : 's'}{/if}
				{#if s.show.status}· {s.show.status}{/if}
			</p>
		</div>
		<div class="mt-3 flex h-2 gap-0.5">
			{#each regular as x (x.season_number)}
				<span class="h-full min-w-1.5 overflow-hidden rounded-full bg-ink/10" style="flex: {Math.max(x.episode_count, 1)}">
					<span
						class="block h-full rounded-full transition-[width] duration-300"
						style="width: {x.episode_count ? (x.watched / x.episode_count) * 100 : 0}%; background: {accent}"
					></span>
				</span>
			{/each}
		</div>
	</section>

	<div class="shelf -mx-4 mt-3 flex gap-2 overflow-x-auto px-4 pb-1 sm:mx-0 sm:px-0 scroll-px-4 sm:scroll-px-0" role="tablist" aria-label="Seasons">
		{#each s.ordered as x (x.season_number)}
			{@const done = x.episode_count > 0 && x.watched >= x.episode_count}
			<button
				type="button"
				role="tab"
				aria-selected={s.selected === x.season_number}
				onclick={() => s.select(x.season_number)}
				class="inline-flex h-11 shrink-0 items-center gap-1.5 rounded-xl border px-3.5 text-sm font-medium whitespace-nowrap transition {s.selected ===
				x.season_number
					? 'border-line-strong bg-ink/10 text-ink'
					: 'border-line bg-surface text-muted hover:text-ink'}"
			>
				{#if x.is_special}<Sparkles class="size-3.5" />{/if}
				{x.is_special ? 'Specials' : `S${x.season_number}`}
				{#if done}
					<Check class="size-3.5" style="color: {accent}" />
				{:else if !x.episode_count}
					<span class="text-[0.75rem] text-faint">TBA</span>
				{:else if x.watched}
					<span class="text-[0.75rem] text-faint tabular-nums">{x.watched}/{x.episode_count}</span>
				{/if}
			</button>
		{/each}
	</div>

	<section class="mt-3" aria-live="polite">
		{#if !season || season.season_number !== s.selected}
			<div class="space-y-2">
				{#each [0, 1, 2, 3] as i (i)}<Skeleton class="h-16 w-full" />{/each}
			</div>
		{:else}
			<div class="mb-2 flex items-center justify-between gap-3">
				<div class="min-w-0">
					<h3 class="truncate text-[0.9375rem] font-semibold">{season.title}</h3>
					<p class="text-xs text-faint">
						{season.watched} of {season.total} watched{season.runtime_minutes ? ` · ${minutesLabel(season.runtime_minutes)}` : ''}
					</p>
				</div>
				{#if season.total}
					<Button
						size="sm"
						icon={allWatched ? RotateCcw : CheckCheck}
						onclick={() => s.markSeason(season.season_number, !allWatched)}
					>
						{allWatched ? 'Unmark season' : 'Mark season'}
					</Button>
				{/if}
			</div>
			{#if season.total}
				<ProgressBar percent={(season.watched / season.total) * 100} {accent} class="mb-3" label="Season progress" />
			{/if}

			{#if season.episodes.length === 0}
				<EmptyState title="No episodes announced yet" class="py-8" />
			{:else}
				<ol class="divide-y divide-line overflow-hidden rounded-[var(--radius-card)] border border-line bg-surface">
					{#each season.episodes as ep (ep.episode_number)}
						<EpisodeItem
							{ep}
							{accent}
							next={nextEp?.season === season.season_number && nextEp.episode.episode_number === ep.episode_number}
							missing={s.missingBefore(season.season_number, ep.episode_number)}
							ontoggle={() => s.toggle(season.season_number, ep)}
							onupto={() => s.upTo(season.season_number, ep.episode_number)}
							onrewatch={() => s.rewatch(season.season_number, ep)}
						/>
					{/each}
				</ol>
			{/if}
		{/if}
	</section>
{/if}
