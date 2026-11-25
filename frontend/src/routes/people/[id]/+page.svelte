<script lang="ts">
	import { ArrowLeft, ExternalLink, UserRound } from '@lucide/svelte';
	import { page } from '$app/state';
	import { get } from '#lib/api.ts';
	import { MEDIA } from '#lib/media.ts';
	import type { Appearance, Person } from '#lib/types.ts';
	import { EmptyState, IconButton, PosterCard, PosterGrid, Skeleton } from '#lib/ui/index.ts';

	let data = $state<{ person: Person; in_library: Appearance[]; elsewhere: Appearance[] } | null>(null);
	let error = $state('');

	$effect(() => {
		const id = page.params.id;
		data = null;
		error = '';
		get<NonNullable<typeof data>>(`/people/${id}`)
			.then((d) => id === page.params.id && (data = d))
			.catch(() => (error = 'Person not found.'));
	});

	const ROLE = { actor: 'as', voice: 'voices', crew: '' } as const;
	const href = (a: Appearance) =>
		a.entry ? `/item/${a.entry.media_type}/${a.entry.id}` : `/media/${a.item.media_type}/${a.item.source}/${a.item.media_id}`;
	const sub = (a: Appearance) => (a.characters.length ? `${ROLE[a.role]} ${a.characters.join(', ')}`.trim() : MEDIA[a.item.media_type].label);
</script>

<svelte:head><title>{data?.person.name ?? 'Person'} · Encompass</title></svelte:head>

<div class="mb-3"><IconButton icon={ArrowLeft} label="Back to people" href="/people" /></div>

{#snippet grid(items: Appearance[])}
	<PosterGrid>
		{#each items as a (a.item.id)}
			<PosterCard href={href(a)} src={a.item.image} title={a.item.title} type={a.item.media_type} subtitle={sub(a)} status={a.entry?.status} />
		{/each}
	</PosterGrid>
{/snippet}

{#if error}
	<EmptyState icon={UserRound} title="Person not found" description={error} />
{:else if !data}
	<div class="flex items-center gap-4" aria-hidden="true"><Skeleton shape="circle" class="size-24" /><Skeleton class="h-8 w-48" /></div>
{:else}
	<header class="mb-8 flex items-center gap-4 sm:gap-5">
		{#if data.person.image}
			<img src={data.person.image} alt="" class="size-24 shrink-0 rounded-full object-cover ring-1 ring-line sm:size-28" />
		{:else}
			<span class="grid size-24 shrink-0 place-items-center rounded-full bg-raised text-faint"><UserRound class="size-9" /></span>
		{/if}
		<div class="min-w-0">
			<h1 class="text-2xl font-semibold tracking-tight sm:text-3xl">{data.person.name}</h1>
			{#if data.person.native_name}<p class="text-muted">{data.person.native_name}</p>{/if}
			<p class="mt-1 text-sm text-faint">In {data.in_library.length} of your titles</p>
			<a href={data.person.url} target="_blank" rel="noreferrer" class="mt-1 inline-flex min-h-11 items-center gap-1 text-[13px] text-muted hover:text-ink">
				Full filmography on {data.person.source === 'tmdb' ? 'TMDB' : 'AniList'} <ExternalLink class="size-3.5" />
			</a>
		</div>
	</header>

	<section>
		<h2 class="mb-3 text-[13px] font-semibold tracking-wide text-faint uppercase">In your library</h2>
		{#if data.in_library.length}{@render grid(data.in_library)}{:else}<p class="text-sm text-muted">Nothing you have watched yet.</p>{/if}
	</section>

	{#if data.elsewhere.length}
		<section class="mt-10">
			<h2 class="mb-3 text-[13px] font-semibold tracking-wide text-faint uppercase">Also in</h2>
			{@render grid(data.elsewhere)}
		</section>
	{/if}
{/if}
