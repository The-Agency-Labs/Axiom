<script lang="ts">
	import { Check, LoaderCircle, Plus, Search } from '@lucide/svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { get, post, qs } from '#lib/api.ts';
	import Cover from '#lib/components/Cover.svelte';
	import { isMediaType, MEDIA, SOURCE_NAMES } from '#lib/media.ts';
	import { enabledTypes } from '#lib/settings.svelte.ts';
	import type { Entry, MediaType, SearchResult } from '#lib/types.ts';

	const initial = page.url.searchParams.get('type') ?? 'tv';
	let type = $state<MediaType>(isMediaType(initial) ? initial : 'tv');
	let query = $state(page.url.searchParams.get('q') ?? '');
	let results = $state<SearchResult[] | null>(null);
	let loading = $state(false);
	let error = $state('');
	let adding = $state<string | null>(null);
	let added = $state(new Set<string>());

	let input: HTMLInputElement;
	$effect(() => input?.focus());

	async function run(event?: SubmitEvent) {
		event?.preventDefault();
		if (!query.trim()) return;
		loading = true;
		error = '';
		history.replaceState(history.state, '', `/search${qs({ type, q: query })}`);
		try {
			const data = await get<{ results: SearchResult[] }>('/search' + qs({ media_type: type, q: query }));
			results = data.results ?? [];
		} catch {
			error = 'Search failed. The provider may be down or rate-limited.';
			results = [];
		} finally {
			loading = false;
		}
	}

	$effect(() => {
		if (query) run();
	});

	const key = (r: SearchResult) => `${r.source}:${r.media_id}`;
	const href = (r: SearchResult) => `/media/${type}/${r.source}/${encodeURIComponent(String(r.media_id))}`;

	async function track(result: SearchResult, status: 'Planning' | 'In progress', open = false) {
		adding = key(result);
		try {
			const entry = await post<Entry>('/library', {
				media_type: type,
				source: result.source,
				media_id: String(result.media_id),
				status
			});
			added = new Set([...added, key(result)]);
			if (open) goto(`/item/${type}/${entry.id}`);
		} catch (e) {
			error = `Could not add ${result.title}.`;
		} finally {
			adding = null;
		}
	}
</script>

<svelte:head><title>Search · Encompass</title></svelte:head>

<h1 class="mb-6 text-3xl font-semibold tracking-tight">Search</h1>

<form onsubmit={run} class="space-y-4">
	<div class="shelf -mx-4 flex gap-1.5 overflow-x-auto px-4 sm:mx-0 sm:px-0">
		{#each enabledTypes() as t (t)}
			{@const meta = MEDIA[t]}
			<button
				type="button"
				onclick={() => {
					type = t;
					if (query) run();
				}}
				class="flex shrink-0 items-center gap-2 rounded-full border px-3.5 py-1.5 text-sm font-medium transition {type === t
					? 'border-line-strong bg-ink/8 text-ink'
					: 'border-transparent text-muted hover:text-ink'}"
			>
				<meta.icon class="size-4" style="color: {meta.accent}" />
				{meta.plural}
			</button>
		{/each}
	</div>

	<label class="relative block">
		<Search class="pointer-events-none absolute top-1/2 left-4 size-5 -translate-y-1/2 text-faint" />
		<input
			bind:this={input}
			bind:value={query}
			placeholder="Search {MEDIA[type].plural.toLowerCase()}…"
			class="w-full rounded-2xl border border-line bg-surface py-3.5 pr-4 pl-12 text-base outline-none transition focus:border-line-strong focus:ring-4 focus:ring-ink/5"
		/>
		{#if loading}
			<LoaderCircle class="absolute top-1/2 right-4 size-5 -translate-y-1/2 animate-spin text-faint" />
		{/if}
	</label>
</form>

{#if error}
	<p class="mt-4 text-sm text-rose-300">{error}</p>
{/if}

{#if results}
	{#if results.length === 0 && !loading}
		<p class="mt-10 text-center text-sm text-muted">No results for “{query}”.</p>
	{:else}
		<div class="mt-8 grid grid-cols-2 gap-x-4 gap-y-7 sm:grid-cols-4 md:grid-cols-5 xl:grid-cols-6">
			{#each results as result (key(result))}
				{@const k = key(result)}
				<div class="group">
					<div class="relative">
						<a href={href(result)} aria-label="Open {result.title}" class="block">
							<Cover
								src={result.image}
								title={result.title}
								{type}
								class="aspect-[2/3] w-full transition duration-300 group-hover:-translate-y-0.5 group-hover:ring-line-strong"
							/>
						</a>
						<div
							class="absolute inset-x-2 bottom-2 flex gap-1.5 opacity-100 transition sm:translate-y-1 sm:opacity-0 sm:group-hover:translate-y-0 sm:group-hover:opacity-100"
						>
							{#if added.has(k)}
								<span class="flex flex-1 items-center justify-center gap-1 rounded-lg bg-emerald-400 py-1.5 text-xs font-semibold text-black">
									<Check class="size-3.5" /> Added
								</span>
							{:else}
								<button
									onclick={() => track(result, 'In progress', true)}
									disabled={adding === k}
									class="flex flex-1 items-center justify-center gap-1 rounded-lg bg-white py-1.5 text-xs font-semibold text-black transition hover:bg-zinc-200"
								>
									{#if adding === k}<LoaderCircle class="size-3.5 animate-spin" />{:else}<Plus class="size-3.5" />{/if}
									Start
								</button>
								<button
									onclick={() => track(result, 'Planning')}
									disabled={adding === k}
									class="rounded-lg bg-black/70 px-2.5 py-1.5 text-xs font-semibold text-white backdrop-blur transition hover:bg-black/90"
								>
									Plan
								</button>
							{/if}
						</div>
					</div>
					<a href={href(result)} class="mt-2 line-clamp-2 block text-[13px] leading-snug font-medium hover:underline">
						{result.title}
					</a>
					<p class="text-[11px] text-faint">{SOURCE_NAMES[result.source] ?? result.source}</p>
				</div>
			{/each}
		</div>
	{/if}
{/if}
