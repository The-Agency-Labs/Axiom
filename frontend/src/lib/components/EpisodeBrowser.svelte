<script lang="ts">
	import { Check, CheckCheck, LoaderCircle, RotateCcw, Sparkles } from '@lucide/svelte';
	import { get, post } from '#lib/api.ts';
	import EpisodeRow from '#lib/components/EpisodeRow.svelte';
	import ProgressBar from '#lib/components/ProgressBar.svelte';
	import { minutesLabel } from '#lib/media.ts';
	import type { SeasonDetail, Show, ShowSeason } from '#lib/types.ts';

	let {
		source,
		mediaId,
		initialSeason = null,
		accent = '#38bdf8',
		onchange = () => {}
	}: {
		source: string;
		mediaId: string;
		initialSeason?: number | null;
		accent?: string;
		onchange?: () => void;
	} = $props();

	let show = $state<Show | null>(null);
	let season = $state<SeasonDetail | null>(null);
	let selected = $state<number | null>(null);
	let loadingSeason = $state(false);
	let busy = $state<Set<number>>(new Set());
	let bulkBusy = $state(false);
	let catchUp = $state<{ upTo: number; missing: number } | null>(null);
	let error = $state('');

	async function loadShow() {
		try {
			show = await get<Show>(`/shows/${source}/${mediaId}`);
		} catch {
			error = 'Could not load seasons.';
			return;
		}
		if (selected === null) selected = initialSeason ?? pickSeason(show.seasons);
	}

	/** Open on the first regular season that still has unwatched episodes. */
	function pickSeason(seasons: ShowSeason[]) {
		const regular = seasons.filter((s) => !s.is_special && s.episode_count);
		return (
			regular.find((s) => s.watched > 0 && s.watched < s.episode_count)?.season_number ??
			regular.find((s) => s.watched < s.episode_count)?.season_number ??
			regular.at(-1)?.season_number ??
			seasons[0]?.season_number ??
			1
		);
	}

	async function loadSeason(number: number) {
		loadingSeason = true;
		catchUp = null;
		try {
			const data = await get<SeasonDetail>(`/shows/${source}/${mediaId}/seasons/${number}`);
			if (number === selected) season = data;
		} finally {
			loadingSeason = false;
		}
	}

	$effect(() => {
		void source;
		void mediaId;
		selected = null;
		season = null;
		loadShow();
	});

	$effect(() => {
		if (selected !== null) loadSeason(selected);
	});

	async function mark(body: Record<string, unknown>) {
		if (selected === null) return;
		season = await post<SeasonDetail>(`/shows/${source}/${mediaId}/seasons/${selected}/episodes`, body);
		await loadShow();
		onchange();
	}

	async function toggle(number: number, watched: boolean) {
		if (!season) return;
		busy = new Set([...busy, number]);
		try {
			await mark({ episodes: [number], watched: !watched });
			if (!watched && season) {
				const missing = season.episodes.filter((e) => e.episode_number < number && !e.watched && e.aired).length;
				catchUp = missing ? { upTo: number, missing } : null;
			} else {
				catchUp = null;
			}
		} finally {
			busy = new Set([...busy].filter((n) => n !== number));
		}
	}

	async function bulk(body: Record<string, unknown>) {
		bulkBusy = true;
		try {
			await mark(body);
			catchUp = null;
		} finally {
			bulkBusy = false;
		}
	}

	const allWatched = $derived(!!season && season.total > 0 && season.watched >= season.total);
	const regular = $derived((show?.seasons ?? []).filter((s) => !s.is_special));
</script>

{#if error}
	<p class="text-sm text-muted">{error}</p>
{:else if show}
	<!-- Series completion -->
	<section class="rounded-2xl border border-line bg-surface p-4 sm:p-5">
		<div class="flex flex-wrap items-end justify-between gap-3">
			<div>
				<p class="text-xs text-muted">Series completion</p>
				<p class="mt-0.5 text-2xl font-semibold tabular-nums">
					{show.completion.percent ?? 0}%
					<span class="ml-1 text-sm font-normal text-muted">
						{show.completion.watched} / {show.completion.total} episodes
					</span>
				</p>
			</div>
			<p class="text-xs text-faint">
				{show.completion.seasons_completed} of {show.completion.seasons} seasons complete
				{#if show.completion.specials_watched}· {show.completion.specials_watched}
					{show.completion.specials_watched === 1 ? 'special' : 'specials'}{/if}
				{#if show.status}· {show.status}{/if}
			</p>
		</div>
		<!-- One segment per season, width by episode count -->
		<div class="mt-4 flex h-2 gap-0.5">
			{#each regular as s (s.season_number)}
				<button
					class="h-full min-w-1.5 overflow-hidden rounded-full bg-white/8 transition hover:brightness-125"
					style="flex: {Math.max(s.episode_count, 1)}"
					title="{s.title}: {s.watched}/{s.episode_count}"
					aria-label="{s.title}: {s.watched} of {s.episode_count} watched"
					onclick={() => (selected = s.season_number)}
				>
					<span
						class="block h-full rounded-full"
						style="width: {s.episode_count ? (s.watched / s.episode_count) * 100 : 0}%; background: {accent}"
					></span>
				</button>
			{/each}
		</div>
	</section>

	<!-- Season picker -->
	<div class="shelf -mx-4 mt-5 flex gap-1.5 overflow-x-auto px-4 pb-1 sm:mx-0 sm:px-0" role="tablist">
		{#each show.seasons as s (s.season_number)}
			{@const done = s.episode_count > 0 && s.watched >= s.episode_count}
			<button
				role="tab"
				aria-selected={selected === s.season_number}
				onclick={() => (selected = s.season_number)}
				class="flex shrink-0 items-center gap-2 rounded-full border px-3.5 py-1.5 text-sm font-medium transition {selected ===
				s.season_number
					? 'border-line-strong bg-white/8 text-ink'
					: 'border-line text-muted hover:text-ink'}"
			>
				{#if s.is_special}<Sparkles class="size-3.5" />{/if}
				{s.is_special ? 'Specials' : s.title.replace(/^Season /, 'S')}
				{#if done}
					<Check class="size-3.5" style="color: {accent}" />
				{:else if !s.episode_count}
					<span class="text-[11px] text-faint">TBA</span>
				{:else if s.watched}
					<span class="text-[11px] text-faint tabular-nums">{s.watched}/{s.episode_count}</span>
				{/if}
			</button>
		{/each}
	</div>

	<!-- Season -->
	<section class="mt-4">
		{#if !season || season.season_number !== selected}
			<div class="space-y-2">
				{#each [0, 1, 2, 3] as i (i)}
					<div class="h-20 animate-pulse rounded-xl bg-white/[0.03]"></div>
				{/each}
			</div>
		{:else}
			<div class="mb-3 flex flex-wrap items-center justify-between gap-3">
				<div>
					<h3 class="text-[15px] font-semibold">{season.title}</h3>
					<p class="text-xs text-faint">
						{season.watched} of {season.total} watched
						{#if season.runtime_minutes}· {minutesLabel(season.runtime_minutes)} total{/if}
						{#if loadingSeason}<LoaderCircle class="ml-1 inline size-3 animate-spin" />{/if}
					</p>
				</div>
				{#if season.total}
					<button
						onclick={() => bulk(allWatched ? { all: true, watched: false } : { all: true })}
						disabled={bulkBusy}
						class="flex items-center gap-1.5 rounded-full border border-line px-3 py-1.5 text-xs font-medium text-muted transition hover:border-line-strong hover:text-ink disabled:opacity-50"
					>
						{#if bulkBusy}
							<LoaderCircle class="size-3.5 animate-spin" />
						{:else if allWatched}
							<RotateCcw class="size-3.5" />
						{:else}
							<CheckCheck class="size-3.5" />
						{/if}
						{allWatched ? 'Unmark season' : 'Mark season watched'}
					</button>
				{/if}
			</div>
			{#if season.total}
				<ProgressBar percent={(season.watched / season.total) * 100} {accent} class="mb-4" />
			{/if}

			{#if catchUp}
				<div
					class="mb-3 flex flex-wrap items-center justify-between gap-2 rounded-xl border px-3.5 py-2.5 text-sm"
					style="border-color: color-mix(in oklab, {accent} 35%, transparent); background: color-mix(in oklab, {accent} 8%, transparent)"
				>
					<span>
						{catchUp.missing} earlier {catchUp.missing === 1 ? 'episode is' : 'episodes are'} unwatched.
					</span>
					<span class="flex gap-2">
						<button class="text-xs text-muted hover:text-ink" onclick={() => (catchUp = null)}>Dismiss</button>
						<button
							class="rounded-full px-3 py-1 text-xs font-semibold text-[#09090b]"
							style="background: {accent}"
							onclick={() => catchUp && bulk({ up_to: catchUp.upTo })}
						>
							Mark E1–E{catchUp.upTo}
						</button>
					</span>
				</div>
			{/if}

			{#if season.episodes.length === 0}
				<p class="rounded-xl border border-dashed border-line-strong p-6 text-center text-sm text-muted">
					No episodes announced yet.
				</p>
			{:else}
				<ol class="divide-y divide-line overflow-hidden rounded-2xl border border-line bg-surface">
					{#each season.episodes as ep (ep.episode_number)}
						<EpisodeRow
							{ep}
							{accent}
							busy={busy.has(ep.episode_number)}
							ontoggle={() => toggle(ep.episode_number, ep.watched)}
							onrewatch={() => mark({ episodes: [ep.episode_number], rewatch: true })}
						/>
					{/each}
				</ol>
			{/if}
		{/if}
	</section>
{/if}
