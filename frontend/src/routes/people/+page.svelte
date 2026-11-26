<script lang="ts">
	import { LoaderCircle, Search, UserRound, UsersRound } from '@lucide/svelte';
	import { get, qs } from '#lib/api.ts';
	import Cover from '#lib/components/Cover.svelte';
	import { MEDIA } from '#lib/media.ts';
	import type { LibraryPerson, Person } from '#lib/types.ts';
	import { Chip, ChipRow, EmptyState, PullToRefresh, Skeleton } from '#lib/ui/index.ts';

	const FILTERS = [
		{ key: 'all', label: 'Everyone', params: {} },
		{ key: 'actor', label: 'Actors', params: { role: 'actor' } },
		{ key: 'voice-ja', label: 'Voice (JA)', params: { role: 'voice', language: 'japanese' } },
		{ key: 'voice-en', label: 'Voice (EN)', params: { role: 'voice', language: 'english' } },
		{ key: 'crew', label: 'Crew', params: { role: 'crew' } }
	] as const;

	let filter = $state<(typeof FILTERS)[number]['key']>('all');
	let data = $state<{ pending: number; library_titles: number; results: LibraryPerson[] } | null>(null);
	let loading = $state(false);
	let query = $state('');
	let found = $state<Person[] | null>(null);

	async function load() {
		loading = true;
		try {
			const params = FILTERS.find((f) => f.key === filter)!.params;
			data = await get('/people' + qs({ ...params, min_titles: 2, limit: 120 }));
		} catch {
			data ??= { pending: 0, library_titles: 0, results: [] };
		} finally {
			loading = false;
		}
	}
	$effect(() => {
		void filter;
		load();
	});

	// While cast lists are still being fetched in the background, check back.
	$effect(() => {
		if (!data?.pending) return;
		const timer = setTimeout(load, 8000);
		return () => clearTimeout(timer);
	});

	// Name search (>= 2 characters), debounced.
	$effect(() => {
		const q = query.trim();
		if (q.length < 2) {
			found = null;
			return;
		}
		const t = setTimeout(() => {
			get<Person[]>('/people/search/by-name' + qs({ q }))
				.then((r) => q === query.trim() && (found = r))
				.catch(() => (found = []));
		}, 300);
		return () => clearTimeout(t);
	});

	const ROLE_LABEL = { actor: 'as', voice: 'voices', crew: '' } as const;
</script>

<svelte:head><title>People · Axiom</title></svelte:head>

<PullToRefresh onrefresh={load}>
	<header class="mb-4">
		<h1 class="text-3xl font-semibold tracking-tight">People</h1>
		<p class="mt-1 text-sm text-muted">Actors, voice actors and crew who show up in more than one thing you have watched.</p>
	</header>

	<div class="relative">
		<Search class="pointer-events-none absolute top-1/2 left-3.5 size-[18px] -translate-y-1/2 text-faint" />
		<input type="search" class="input pl-10" placeholder="Find a person by name" aria-label="Find a person by name" bind:value={query} autocomplete="off" />
	</div>

	{#if found !== null}
		<section class="mt-4" aria-label="Name search results">
			{#if found.length === 0}
				<p class="rounded-2xl border border-dashed border-line-strong p-5 text-sm text-muted">Nobody called "{query}" in the catalog yet.</p>
			{:else}
				<ul class="divide-y divide-line overflow-hidden rounded-[var(--radius-card)] border border-line bg-surface">
					{#each found as p (p.id)}
						<li>
							<a href="/people/{p.id}" class="flex min-h-14 items-center gap-3 px-4 py-2 hover:bg-ink/5">
								{#if p.image}<img src={p.image} alt="" loading="lazy" class="size-10 rounded-full object-cover" />{:else}<span class="grid size-10 place-items-center rounded-full bg-raised text-faint"><UserRound class="size-5" /></span>{/if}
								<span class="min-w-0 flex-1">
									<span class="block truncate text-[0.9375rem] font-medium">{p.name}</span>
									{#if p.native_name}<span class="block truncate text-[0.8125rem] text-muted">{p.native_name}</span>{/if}
								</span>
							</a>
						</li>
					{/each}
				</ul>
			{/if}
		</section>
	{:else}
		<ChipRow label="People filters">
			{#each FILTERS as f (f.key)}
				<Chip selected={filter === f.key} onclick={() => (filter = f.key)}>{f.label}</Chip>
			{/each}
			{#if loading}<LoaderCircle class="ml-1 size-4 shrink-0 animate-spin self-center text-faint" />{/if}
		</ChipRow>

		{#if data?.pending}
			<p class="mb-4 flex items-center gap-2 rounded-xl border border-line bg-surface px-4 py-2.5 text-sm text-muted">
				<LoaderCircle class="size-4 shrink-0 animate-spin" />
				Fetching cast for {data.pending} of {data.library_titles} titles…
			</p>
		{/if}

		{#if !data}
			<div class="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3" aria-hidden="true">
				{#each Array(6) as _, i (i)}<Skeleton class="h-32 w-full" />{/each}
			</div>
		{:else if data.results.length === 0}
			<EmptyState
				icon={UsersRound}
				title={data.pending ? 'Still gathering cast lists…' : 'No recurring people yet'}
				description={data.pending ? undefined : 'People who appear in two or more of your titles will show up here.'}
			/>
		{:else}
			<div class="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3">
				{#each data.results as row (row.person.id)}
					<a
						href="/people/{row.person.id}"
						class="flex min-w-0 gap-3 rounded-[var(--radius-card)] border border-line bg-surface p-3 transition hover:border-line-strong hover:bg-raised"
					>
						{#if row.person.image}
							<img src={row.person.image} alt="" loading="lazy" class="h-24 w-16 shrink-0 rounded-lg object-cover" />
						{:else}
							<span class="grid h-24 w-16 shrink-0 place-items-center rounded-lg bg-raised text-faint"><UserRound class="size-6" /></span>
						{/if}
						<div class="min-w-0 flex-1">
							<p class="truncate font-medium">{row.person.name}</p>
							<p class="truncate text-xs text-faint">{row.titles} titles{row.person.native_name ? ` · ${row.person.native_name}` : ''}</p>
							<ul class="mt-2 space-y-1.5">
								{#each row.appearances.slice(0, 3) as a (a.item.id)}
									{@const meta = MEDIA[a.item.media_type]}
									<li class="flex items-center gap-2 text-xs">
										<Cover src={a.item.image} title={a.item.title} type={a.item.media_type} class="aspect-[2/3] w-5 shrink-0 !rounded-sm" />
										<span class="min-w-0 truncate">
											<span>{a.item.title}</span>
											{#if a.characters.length}<span class="text-faint"> {ROLE_LABEL[a.role]} {a.characters.join(', ')}</span>{/if}
										</span>
										<meta.icon class="ml-auto size-3 shrink-0" style="color: {meta.accent}" />
									</li>
								{/each}
								{#if row.appearances.length > 3}<li class="text-[0.75rem] text-faint">+{row.appearances.length - 3} more</li>{/if}
							</ul>
						</div>
					</a>
				{/each}
			</div>
		{/if}
	{/if}
</PullToRefresh>
