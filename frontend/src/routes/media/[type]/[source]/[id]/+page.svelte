<script lang="ts">
	/**
	 * Preview of anything from a provider (e.g. a search result).
	 * Tracked items forward to their library page; untracked ones can be
	 * started or planned here, and TV shows can be browsed episode by episode.
	 */
	import { ArrowLeft, BookmarkPlus, LoaderCircle, Play } from '@lucide/svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { get, post, qs } from '#lib/api.ts';
	import AnimeEpisodes from '#lib/components/AnimeEpisodes.svelte';
	import CastSection from '#lib/components/CastSection.svelte';
	import EpisodeBrowser from '#lib/components/EpisodeBrowser.svelte';
	import MediaHero from '#lib/components/MediaHero.svelte';
	import { MEDIA } from '#lib/media.ts';
	import type { Entry, MediaType } from '#lib/types.ts';

	const type = $derived(page.params.type as MediaType);
	const source = $derived(page.params.source!);
	const mediaId = $derived(page.params.id!);
	const meta = $derived(MEDIA[type] ?? MEDIA.tv);

	let md = $state<Record<string, any> | null>(null);
	let error = $state('');
	let adding = $state<string | null>(null);

	async function openIfTracked() {
		const found = await get<{ id: number | null }>(
			'/library/find' + qs({ media_type: type, source, media_id: mediaId })
		);
		if (found.id) {
			goto(`/item/${type}/${found.id}`, { replaceState: true });
			return true;
		}
		return false;
	}

	$effect(() => {
		void type;
		void source;
		void mediaId;
		md = null;
		error = '';
		(async () => {
			try {
				if (await openIfTracked()) return;
				md = await get<Record<string, any>>(`/metadata/${type}/${source}/${mediaId}`);
			} catch {
				error = 'Could not load this title.';
			}
		})();
	});

	async function track(status: 'In progress' | 'Planning' | 'Completed') {
		adding = status;
		try {
			const entry = await post<Entry>('/library', { media_type: type, source, media_id: mediaId, status });
			goto(`/item/${type}/${entry.id}`, { replaceState: true });
		} catch {
			error = 'Could not add it to your library.';
		} finally {
			adding = null;
		}
	}

	const actions = $derived(
		type === 'movie'
			? ([
					['Completed', 'Watched'],
					['Planning', 'Watchlist']
				] as const)
			: ([
					['In progress', 'Start'],
					['Planning', 'Plan']
				] as const)
	);
</script>

<svelte:head><title>{md?.title ?? 'Loading'} · Encompass</title></svelte:head>

<button onclick={() => history.back()} class="mb-6 flex items-center gap-1.5 text-sm text-muted transition hover:text-ink">
	<ArrowLeft class="size-4" /> Back
</button>

{#if error}
	<p class="text-muted">{error}</p>
{:else if !md}
	<div class="flex gap-8">
		<div class="aspect-[2/3] w-48 animate-pulse rounded-2xl bg-white/5"></div>
		<div class="flex-1 space-y-3">
			<div class="h-8 w-2/3 animate-pulse rounded bg-white/5"></div>
			<div class="h-4 w-1/3 animate-pulse rounded bg-white/5"></div>
		</div>
	</div>
{:else}
	<MediaHero {type} title={md.title} image={md.image} {md}>
		<div class="mt-6 flex flex-wrap items-center gap-2">
			{#each actions as [status, label], i (status)}
				<button
					onclick={() => track(status)}
					disabled={adding !== null}
					class="flex items-center gap-2 rounded-full px-5 py-2.5 text-sm font-semibold transition disabled:opacity-60 {i === 0
						? 'text-[#09090b]'
						: 'border border-line bg-surface hover:border-line-strong'}"
					style={i === 0 ? `background: ${meta.accent}` : ''}
				>
					{#if adding === status}
						<LoaderCircle class="size-4 animate-spin" />
					{:else if i === 0}
						<Play class="size-4" />
					{:else}
						<BookmarkPlus class="size-4" />
					{/if}
					{label}
				</button>
			{/each}
			<span class="text-xs text-faint">Not in your library yet</span>
		</div>
	</MediaHero>

	<div class="mt-10 max-w-4xl space-y-8">
		<!-- Marking an episode tracks the title; then switch to its library page. -->
		{#if type === 'tv'}
			<section>
				<h2 class="mb-3 text-[15px] font-semibold">Episodes</h2>
				<EpisodeBrowser {source} {mediaId} accent={meta.accent} onchange={openIfTracked} />
			</section>
		{:else if type === 'anime' && ['mal', 'anilist'].includes(source)}
			<section>
				<h2 class="mb-3 text-[15px] font-semibold">Episodes</h2>
				<AnimeEpisodes {source} {mediaId} accent={meta.accent} onchange={openIfTracked} />
			</section>
		{/if}
		{#if ['tv', 'movie', 'anime'].includes(type)}
			<CastSection mediaType={type} {source} {mediaId} accent={meta.accent} />
		{/if}
	</div>
{/if}
