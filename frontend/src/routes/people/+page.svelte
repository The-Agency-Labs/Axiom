<script lang="ts">
	import { LoaderCircle, UserRound } from '@lucide/svelte';
	import { get, qs } from '#lib/api.ts';
	import Cover from '#lib/components/Cover.svelte';
	import { MEDIA } from '#lib/media.ts';
	import type { LibraryPerson } from '#lib/types.ts';

	const FILTERS = [
		{ key: 'all', label: 'Everyone', params: {} },
		{ key: 'actor', label: 'Actors', params: { role: 'actor' } },
		{ key: 'voice-ja', label: 'Voice · Japanese', params: { role: 'voice', language: 'japanese' } },
		{ key: 'voice-en', label: 'Voice · English', params: { role: 'voice', language: 'english' } },
		{ key: 'crew', label: 'Directors & crew', params: { role: 'crew' } }
	] as const;

	let filter = $state<(typeof FILTERS)[number]['key']>('all');
	let data = $state<{ pending: number; library_titles: number; results: LibraryPerson[] } | null>(null);
	let loading = $state(false);

	async function load() {
		loading = true;
		try {
			const params = FILTERS.find((f) => f.key === filter)!.params;
			data = await get('/people' + qs({ ...params, min_titles: 2, limit: 120 }));
		} finally {
			loading = false;
		}
	}

	$effect(() => {
		void filter;
		load();
	});

	// While cast is still being fetched in the background, check back.
	$effect(() => {
		if (!data?.pending) return;
		const timer = setTimeout(load, 8000);
		return () => clearTimeout(timer);
	});

	const ROLE_LABEL = { actor: 'as', voice: 'voices', crew: '' } as const;
</script>

<svelte:head><title>People · Encompass</title></svelte:head>

<header class="mb-6">
	<h1 class="text-3xl font-semibold tracking-tight">People</h1>
	<p class="mt-1 text-sm text-muted">Actors, voice actors and directors who show up in more than one thing you've watched.</p>
</header>

<div class="shelf -mx-4 mb-6 flex gap-1.5 overflow-x-auto px-4 sm:mx-0 sm:px-0">
	{#each FILTERS as f (f.key)}
		<button
			onclick={() => (filter = f.key)}
			class="shrink-0 rounded-full border px-3.5 py-1.5 text-sm font-medium transition {filter === f.key
				? 'border-line-strong bg-ink/8 text-ink'
				: 'border-transparent text-muted hover:text-ink'}"
		>
			{f.label}
		</button>
	{/each}
	{#if loading}<LoaderCircle class="ml-2 size-4 shrink-0 animate-spin self-center text-faint" />{/if}
</div>

{#if data?.pending}
	<p class="mb-4 flex items-center gap-2 rounded-xl border border-line bg-surface px-4 py-2.5 text-sm text-muted">
		<LoaderCircle class="size-4 animate-spin" />
		Fetching cast for {data.pending} of {data.library_titles} titles…
	</p>
{/if}

{#if !data}
	<div class="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
		{#each Array(6) as _, i (i)}<div class="h-36 animate-pulse rounded-2xl bg-ink/[0.03]"></div>{/each}
	</div>
{:else if data.results.length === 0}
	<div class="rounded-2xl border border-dashed border-line-strong p-10 text-center text-sm text-muted">
		{data.pending ? 'Still gathering cast lists…' : 'Nobody shows up in more than one of your titles yet.'}
	</div>
{:else}
	<div class="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
		{#each data.results as row (row.person.id)}
			<a
				href="/people/{row.person.id}"
				class="flex gap-4 rounded-2xl border border-line bg-surface p-4 transition hover:border-line-strong hover:bg-raised"
			>
				{#if row.person.image}
					<img src={row.person.image} alt="" loading="lazy" class="h-24 w-16 shrink-0 rounded-lg object-cover" />
				{:else}
					<span class="grid h-24 w-16 shrink-0 place-items-center rounded-lg bg-ink/5 text-faint"><UserRound class="size-6" /></span>
				{/if}
				<div class="min-w-0 flex-1">
					<p class="truncate font-medium">{row.person.name}</p>
					<p class="text-xs text-faint">{row.titles} titles{row.person.native_name ? ` · ${row.person.native_name}` : ''}</p>
					<ul class="mt-2 space-y-1">
						{#each row.appearances.slice(0, 3) as a (a.item.id)}
							{@const meta = MEDIA[a.entry?.media_type ?? a.item.media_type]}
							<li class="flex items-center gap-2 text-xs">
								<Cover src={a.item.image} title={a.item.title} type={a.item.media_type} class="aspect-[2/3] w-5 shrink-0 rounded-sm" />
								<span class="min-w-0 truncate">
									<span class="text-ink">{a.item.title}</span>
									{#if a.characters.length}<span class="text-faint"> {ROLE_LABEL[a.role]} {a.characters.join(', ')}</span>{/if}
								</span>
								<meta.icon class="ml-auto size-3 shrink-0" style="color: {meta.accent}" />
							</li>
						{/each}
						{#if row.appearances.length > 3}
							<li class="text-[11px] text-faint">+{row.appearances.length - 3} more</li>
						{/if}
					</ul>
				</div>
			</a>
		{/each}
	</div>
{/if}
