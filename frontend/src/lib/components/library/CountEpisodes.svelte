<script lang="ts">
	import { get, mutate } from '#lib/api.ts';
	import { minutesLabel } from '#lib/media.ts';
	import type { AnimeEpisodes } from '#lib/types.ts';
	import { Button, EmptyState, ProgressBar, Skeleton } from '#lib/ui/index.ts';
	import EpisodeItem from './EpisodeItem.svelte';

	/**
	 * Episode list of a count-based anime (MAL/AniList only). Ticking episode N sets progress to N,
	 * unticking sets it to N-1, as on MyAnimeList and AniList; a manual tap may lower progress.
	 */
	let {
		source,
		mediaId,
		accent,
		onchange
	}: { source: string; mediaId: string; accent: string; onchange?: () => void } = $props();

	const PAGE = 24;
	let data = $state<AnimeEpisodes | null>(null);
	let error = $state(false);
	let showAll = $state(false);
	const path = $derived(`/anime/${source}/${mediaId}/episodes`);

	$effect(() => {
		void path;
		data = null;
		error = false;
		showAll = false;
		get<AnimeEpisodes>(path, (d) => (data = d))
			.then((d) => (data = d))
			.catch(() => (error = true));
	});

	function toggle(number: number, watched: boolean) {
		const d = data;
		if (!d) return;
		const before = { progress: d.progress, flags: d.episodes.map((e) => [e.watched, e.watch_count] as const) };
		mutate<AnimeEpisodes>({
			path,
			body: { episode: number, watched: !watched },
			label: `Episode ${number} ${watched ? 'unmarked' : 'watched'}`,
			optimistic: () => {
				const target = watched ? number - 1 : number;
				d.progress = target;
				d.episodes.forEach((e) => {
					e.watched = e.episode_number <= target;
					e.watch_count = e.watched ? 1 : 0;
				});
				return () => {
					d.progress = before.progress;
					d.episodes.forEach((e, i) => ([e.watched, e.watch_count] = before.flags[i]));
				};
			},
			onSuccess: (fresh) => {
				data = fresh;
				onchange?.();
			}
		});
	}

	// Long runners open around the next episode instead of at episode 1.
	const visible = $derived.by(() => {
		if (!data) return [];
		if (showAll || data.episodes.length <= PAGE) return data.episodes;
		const start = Math.max(0, Math.min(data.progress - 3, data.episodes.length - PAGE));
		return data.episodes.slice(start, start + PAGE);
	});
	const nextNumber = $derived(data?.episodes.find((e) => !e.watched && e.aired)?.episode_number ?? null);
</script>

{#if error}
	<EmptyState title="Could not load episodes" class="py-8" />
{:else if !data}
	<div class="space-y-2">
		{#each [0, 1, 2, 3] as i (i)}<Skeleton class="h-16 w-full" />{/each}
	</div>
{:else if data.episodes.length > 1}
	<p class="mb-2 text-xs text-faint">
		{data.progress} of {data.total} watched{data.runtime_minutes ? ` · ${minutesLabel(data.runtime_minutes)}` : ''}
	</p>
	<ProgressBar percent={data.total ? (data.progress / data.total) * 100 : 0} {accent} class="mb-3" label="Episode progress" />
	{#if data.episodes.length > visible.length}
		<Button size="sm" class="mb-2" onclick={() => (showAll = true)}>Show all {data.episodes.length} episodes</Button>
	{/if}
	<ol class="divide-y divide-line overflow-hidden rounded-[var(--radius-card)] border border-line bg-surface">
		{#each visible as ep (ep.episode_number)}
			<EpisodeItem {ep} {accent} next={ep.episode_number === nextNumber} ontoggle={() => toggle(ep.episode_number, ep.watched)} />
		{/each}
	</ol>
	<p class="mt-2 text-xs text-faint">Ticking an episode marks everything before it too.</p>
{/if}
