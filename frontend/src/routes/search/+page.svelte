<script lang="ts">
	import { Check, LayoutGrid, Plus, Search, SearchX, X } from '@lucide/svelte';
	import { page } from '$app/state';
	import { get, qs } from '#lib/api.ts';
	import Cover from '#lib/components/Cover.svelte';
	import TrackSheet from '#lib/components/library/TrackSheet.svelte';
	import { isMediaType, MEDIA, SOURCE_NAMES, titleHref } from '#lib/media.ts';
	import { net } from '#lib/offline.svelte.ts';
	import { enabledTypes } from '#lib/settings.svelte.ts';
	import type { MediaType, SearchResult } from '#lib/types.ts';
	import { Button, Chip, ChipRow, EmptyState, IconButton, ListRow, Skeleton } from '#lib/ui/index.ts';

	interface SearchPage {
		results?: SearchResult[];
		page?: number;
		total_pages?: number;
	}

	const types = $derived(enabledTypes());
	const initial = page.url.searchParams.get('type') ?? '';
	/** 'all' (the default) searches every enabled type at once; a type chip narrows it. */
	let type = $state<MediaType | 'all'>(isMediaType(initial) ? initial : 'all');
	let query = $state(page.url.searchParams.get('q') ?? '');
	let results = $state<SearchResult[] | null>(null);
	let pages = $state({ page: 1, total: 1 });
	let loading = $state(false);
	let error = $state(false);
	let added = $state(new Set<string>());
	let trackFor = $state<SearchResult | null>(null);
	let trackOpen = $state(false);
	let seq = 0;

	// Fall back to the first enabled type when the remembered one is switched off.
	$effect(() => {
		if (types.length && type !== 'all' && !types.includes(type)) type = 'all';
	});

	const key = (r: SearchResult) => `${r.media_type}:${r.source}:${r.media_id}`;

	async function run(next = 1) {
		const q = query.trim();
		const mine = ++seq;
		if (!q) {
			results = null;
			loading = false;
			error = false;
			return;
		}
		loading = true;
		error = false;
		try {
			const data = await get<SearchPage>(
				'/search' + qs({ media_type: type, q, page: next, types: type === 'all' ? types.join(',') : undefined })
			);
			if (mine !== seq) return;
			const list = (data.results ?? []).map((r) => ({ ...r, media_type: r.media_type ?? (type === 'all' ? 'tv' : type) }));
			results = next > 1 ? [...(results ?? []), ...list] : list;
			pages = { page: next, total: data.total_pages ?? 1 };
		} catch {
			if (mine !== seq) return;
			error = true;
			if (next === 1) results = [];
		} finally {
			if (mine === seq) loading = false;
		}
	}

	// Debounced as you type; type chips re-run it at once.
	$effect(() => {
		void type;
		const q = query;
		try {
			history.replaceState(history.state, '', `/search${qs({ type, q })}`);
		} catch {
			/* router not ready */
		}
		if (!q.trim()) {
			void run();
			return;
		}
		loading = true;
		const timer = setTimeout(() => run(), 350);
		return () => clearTimeout(timer);
	});

	let input: HTMLInputElement;
	$effect(() => {
		if (matchMedia('(pointer: fine)').matches) input?.focus();
	});

	const what = $derived(type === 'all' ? 'everything' : MEDIA[type].plural.toLowerCase());
	const subtitle = (r: SearchResult) =>
		[r.release_year, MEDIA[r.media_type]?.label ?? r.media_type, SOURCE_NAMES[r.source] ?? r.source].filter(Boolean).join(' · ');
</script>

<svelte:head><title>Search · Encompass</title></svelte:head>

<h1 class="sr-only">Search</h1>

<form
	role="search"
	onsubmit={(e) => {
		e.preventDefault();
		run();
	}}
	class="relative"
>
	<Search class="pointer-events-none absolute top-1/2 left-4 size-5 -translate-y-1/2 text-faint" />
	<input
		bind:this={input}
		bind:value={query}
		type="search"
		enterkeyhint="search"
		autocomplete="off"
		aria-label="Search {what}"
		placeholder="Search {what}"
		class="h-12 w-full rounded-2xl border border-line bg-surface pr-12 pl-12 outline-none transition focus:border-line-strong focus:ring-4 focus:ring-ink/5"
	/>
	{#if query}
		<button
			type="button"
			class="absolute top-0.5 right-0.5 grid size-11 place-items-center rounded-full text-faint hover:text-ink"
			aria-label="Clear"
			onclick={() => {
				query = '';
				input?.focus();
			}}><X class="size-5" /></button
		>
	{/if}
</form>

<ChipRow label="Filter by type">
	<Chip selected={type === 'all'} icon={LayoutGrid} onclick={() => (type = 'all')}>All</Chip>
	{#each types as t (t)}
		{@const m = MEDIA[t]}
		<Chip selected={type === t} icon={m.icon} color={m.accent} onclick={() => (type = t)}>{m.plural}</Chip>
	{/each}
</ChipRow>

<div class="mt-2">
	{#if !net.online}
		<EmptyState icon={SearchX} title="You are offline" description="Search needs a connection. Your library still works." />
	{:else if results === null && !loading}
		<EmptyState icon={Search} title="Find something to track" description="Search {what} by title." class="py-16" />
	{:else if results === null || (loading && !results?.length)}
		<div class="space-y-1" aria-busy="true">
			{#each Array(6) as _, i (i)}
				<div class="flex items-center gap-3 px-4 py-2.5">
					<Skeleton class="aspect-[2/3] w-12 shrink-0" />
					<div class="flex-1"><Skeleton shape="text" lines={2} /></div>
				</div>
			{/each}
		</div>
	{:else if results.length === 0}
		<EmptyState
			icon={SearchX}
			title={error ? 'Search failed' : `No results for "${query}"`}
			description={error ? 'The provider may be down or rate-limited. Try again in a moment.' : 'Check the spelling or try a different filter.'}
		>
			{#snippet actions()}{#if error}<Button variant="primary" onclick={() => run()}>Retry</Button>{/if}{/snippet}
		</EmptyState>
	{:else}
		<ul class="divide-y divide-line overflow-hidden rounded-[var(--radius-card)] border border-line bg-surface {loading ? 'opacity-60' : ''}">
			{#each results as r (key(r))}
				<li class="flex items-center pr-2">
					<ListRow href={titleHref(r, r.media_type)} title={r.title} subtitle={subtitle(r)} class="min-w-0 flex-1">
						{#snippet leading()}
							<Cover src={r.image} title={r.title} type={r.media_type} class="aspect-[2/3] w-12 shrink-0 rounded-lg" />
						{/snippet}
					</ListRow>
					{#if added.has(key(r))}
						<span class="grid size-11 shrink-0 place-items-center text-success" role="img" aria-label="Tracked"><Check class="size-5" /></span>
					{:else}
						<IconButton
							icon={Plus}
							label="Track {r.title}"
							variant="surface"
							onclick={() => {
								trackFor = r;
								trackOpen = true;
							}}
						/>
					{/if}
				</li>
			{/each}
		</ul>
		{#if pages.page < pages.total}
			<div class="mt-3 flex justify-center">
				<Button loading={loading} onclick={() => run(pages.page + 1)}>Load more</Button>
			</div>
		{/if}
	{/if}
</div>

<TrackSheet bind:open={trackOpen} result={trackFor} ontracked={(r) => (added = new Set([...added, key(r)]))} />
