<script lang="ts">
	import { ArrowLeft, ExternalLink, UserRound } from '@lucide/svelte';
	import { page } from '$app/state';
	import { get } from '#lib/api.ts';
	import Cover from '#lib/components/Cover.svelte';
	import { MEDIA, STATUS_COLOR } from '#lib/media.ts';
	import type { Appearance, Person } from '#lib/types.ts';

	const id = $derived(page.params.id);
	let data = $state<{ person: Person; in_library: Appearance[]; elsewhere: Appearance[] } | null>(null);
	let error = $state('');

	$effect(() => {
		void id;
		data = null;
		get<typeof data>(`/people/${id}`)
			.then((d) => (data = d))
			.catch(() => (error = 'Person not found.'));
	});

	const ROLE = { actor: 'as', voice: 'voices', crew: '' } as const;
	const href = (a: Appearance) =>
		a.entry ? `/item/${a.entry.media_type}/${a.entry.id}` : `/media/${a.item.media_type}/${a.item.source}/${a.item.media_id}`;
	const sourceName = (p: Person) => (p.source === 'tmdb' ? 'TMDB' : 'AniList');
</script>

<svelte:head><title>{data?.person.name ?? 'Person'} · Encompass</title></svelte:head>

<button onclick={() => history.back()} class="mb-6 flex items-center gap-1.5 text-sm text-muted transition hover:text-ink">
	<ArrowLeft class="size-4" /> Back
</button>

{#snippet grid(items: Appearance[])}
	<div class="grid grid-cols-3 gap-x-3 gap-y-5 sm:grid-cols-5 lg:grid-cols-7">
		{#each items as a (a.item.id)}
			{@const meta = MEDIA[a.item.media_type]}
			<a href={href(a)} class="group">
				<div class="relative">
					<Cover src={a.item.image} title={a.item.title} type={a.item.media_type} class="aspect-[2/3] w-full transition group-hover:ring-line-strong" />
					{#if a.character_images?.[0]}
						<img
							src={a.character_images[0]}
							alt=""
							loading="lazy"
							class="absolute top-1.5 right-1.5 size-9 rounded-full object-cover ring-2 ring-[var(--color-canvas)]"
						/>
					{/if}
				</div>
				<p class="mt-2 line-clamp-1 text-xs font-medium">{a.item.title}</p>
				{#if a.characters.length}
					<p class="line-clamp-2 text-[11px] leading-snug text-faint">{ROLE[a.role]} {a.characters.join(', ')}</p>
				{/if}
				<p class="mt-0.5 flex items-center gap-1 text-[10px] text-faint">
					<meta.icon class="size-3" style="color: {meta.accent}" />
					{#if a.entry}
						<span class="size-1.5 rounded-full" style="background: {STATUS_COLOR[a.entry.status]}"></span>{a.entry.status}
					{:else}
						{meta.label}
					{/if}
				</p>
			</a>
		{/each}
	</div>
{/snippet}

{#if error}
	<p class="text-muted">{error}</p>
{:else if !data}
	<div class="flex items-center gap-5">
		<div class="size-28 animate-pulse rounded-full bg-ink/5"></div>
		<div class="h-8 w-48 animate-pulse rounded bg-ink/5"></div>
	</div>
{:else}
	<header class="mb-10 flex items-center gap-5">
		{#if data.person.image}
			<div class="size-24 shrink-0 overflow-hidden rounded-full ring-1 ring-line sm:size-28">
				<img src={data.person.image} alt="" class="h-full w-full object-cover" />
			</div>
		{:else}
			<span class="grid size-28 shrink-0 place-items-center rounded-full bg-ink/5 text-faint"><UserRound class="size-10" /></span>
		{/if}
		<div class="min-w-0">
			<h1 class="text-3xl font-semibold tracking-tight">{data.person.name}</h1>
			{#if data.person.native_name}<p class="text-muted">{data.person.native_name}</p>{/if}
			<p class="mt-1 text-sm text-faint">In {data.in_library.length} of your titles</p>
			<a
				href={data.person.url}
				target="_blank"
				rel="noreferrer"
				class="mt-2 inline-flex items-center gap-1 text-xs text-muted hover:text-ink"
			>
				Full filmography on {sourceName(data.person)} <ExternalLink class="size-3" />
			</a>
		</div>
	</header>

	<section>
		<h2 class="mb-4 text-xs font-semibold tracking-wider text-faint uppercase">In your library</h2>
		{#if data.in_library.length}
			{@render grid(data.in_library)}
		{:else}
			<p class="text-sm text-muted">Nothing you've watched yet.</p>
		{/if}
	</section>

	{#if data.elsewhere.length}
		<section class="mt-10">
			<h2 class="mb-4 text-xs font-semibold tracking-wider text-faint uppercase">Also in</h2>
			{@render grid(data.elsewhere)}
		</section>
	{/if}
{/if}
