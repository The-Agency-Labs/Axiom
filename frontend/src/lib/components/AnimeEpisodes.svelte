<script lang="ts">
	import { LoaderCircle } from '@lucide/svelte';
	import { get, post } from '#lib/api.ts';
	import EpisodeRow from '#lib/components/EpisodeRow.svelte';
	import ProgressBar from '#lib/components/ProgressBar.svelte';
	import { minutesLabel } from '#lib/media.ts';
	import type { AnimeEpisodes } from '#lib/types.ts';

	/** Anime episode list; ticking episode N sets progress to N (like MAL/AniList). */
	let {
		source,
		mediaId,
		accent = '#a78bfa',
		onchange = () => {}
	}: { source: string; mediaId: string; accent?: string; onchange?: () => void } = $props();

	let data = $state<AnimeEpisodes | null>(null);
	let error = $state('');
	let busy = $state<number | null>(null);
	let showAll = $state(false);
	const PAGE = 26;

	$effect(() => {
		void source;
		void mediaId;
		data = null;
		error = '';
		get<AnimeEpisodes>(`/anime/${source}/${mediaId}/episodes`)
			.then((d) => (data = d))
			.catch(() => (error = 'Could not load episodes.'));
	});

	async function toggle(episode: number, watched: boolean) {
		busy = episode;
		try {
			data = await post<AnimeEpisodes>(`/anime/${source}/${mediaId}/episodes`, { episode, watched: !watched });
			onchange();
		} finally {
			busy = null;
		}
	}

	// Long-runners: open around the next episode instead of at episode 1.
	const visible = $derived.by(() => {
		if (!data) return [];
		if (showAll || data.episodes.length <= PAGE) return data.episodes;
		const start = Math.max(0, Math.min(data.progress - 3, data.episodes.length - PAGE));
		return data.episodes.slice(start, start + PAGE);
	});
	const hidden = $derived(data ? data.episodes.length - visible.length : 0);
	const providerNote = $derived(
		data?.providers.length ? `Episode details from ${data.providers.map((p) => (p === 'tmdb' ? 'TMDB' : 'AniList')).join(' + ')}` : ''
	);
</script>

{#if error}
	<p class="text-sm text-muted">{error}</p>
{:else if !data}
	<div class="space-y-2">
		{#each [0, 1, 2, 3] as i (i)}<div class="h-20 animate-pulse rounded-xl bg-white/[0.03]"></div>{/each}
	</div>
{:else if data.episodes.length > 1}
	<div class="mb-3 flex flex-wrap items-end justify-between gap-2">
		<p class="text-xs text-faint">
			{data.progress} of {data.total} watched
			{#if data.runtime_minutes}· {minutesLabel(data.runtime_minutes)} total{/if}
			{#if busy !== null}<LoaderCircle class="ml-1 inline size-3 animate-spin" />{/if}
		</p>
		{#if providerNote}<p class="text-[11px] text-faint">{providerNote}</p>{/if}
	</div>
	<ProgressBar percent={data.total ? (data.progress / data.total) * 100 : 0} {accent} class="mb-4" />
	{#if hidden && !showAll}
		<button class="mb-2 text-xs text-muted hover:text-ink" onclick={() => (showAll = true)}>
			Show all {data.episodes.length} episodes
		</button>
	{/if}
	<ol class="divide-y divide-line overflow-hidden rounded-2xl border border-line bg-surface">
		{#each visible as ep (ep.episode_number)}
			<EpisodeRow {ep} {accent} busy={busy === ep.episode_number} ontoggle={() => toggle(ep.episode_number, ep.watched)} />
		{/each}
	</ol>
	<p class="mt-2 text-[11px] text-faint">Ticking an episode marks everything before it too, as on MyAnimeList and AniList.</p>
{/if}
