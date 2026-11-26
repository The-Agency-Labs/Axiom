<script lang="ts">
	import { Globe, ListPlus, Lock, Users } from '@lucide/svelte';
	import { goto } from '$app/navigation';
	import { get, mutate, qs } from '#lib/api.ts';
	import Cover from '#lib/components/Cover.svelte';
	import ListFormSheet from '#lib/components/reading/ListFormSheet.svelte';
	import type { ListDetail, ListSummary } from '#lib/components/reading/types.ts';
	import { Button, Chip, ChipRow, EmptyState, PullToRefresh, Skeleton } from '#lib/ui/index.ts';

	const SORTS = [
		{ key: 'last_item_added', label: 'Recent' },
		{ key: 'name', label: 'Name' },
		{ key: 'items_count', label: 'Most items' },
		{ key: 'newest_first', label: 'Newest' }
	];
	let sort = $state('last_item_added');
	let query = $state('');
	let lists = $state<ListSummary[] | null>(null);
	let createOpen = $state(false);
	let creating = $state(false);

	async function load() {
		const s = sort;
		const q = query.trim();
		try {
			const d = await get<ListSummary[]>('/lists' + qs({ sort: s, q }));
			if (s === sort && q === query.trim()) lists = d;
		} catch {
			lists ??= [];
		}
	}
	$effect(() => {
		void sort;
		const t = setTimeout(load, query ? 250 : 0);
		return () => clearTimeout(t);
	});

	const owned = $derived(lists?.filter((l) => l.is_owner) ?? []);
	const shared = $derived(lists?.filter((l) => !l.is_owner) ?? []);

	async function create(v: { name: string; description: string; visibility: 'private' | 'public' }) {
		creating = true;
		const res = await mutate<ListDetail>({ method: 'POST', path: '/lists', body: v, label: 'List created' });
		creating = false;
		if (res.ok && res.data) {
			createOpen = false;
			goto(`/lists/${res.data.id}`);
		}
	}
</script>

{#snippet card(l: ListSummary)}
	<li>
		<a
			href="/lists/{l.id}"
			class="flex items-center gap-3 rounded-[var(--radius-card)] border border-line bg-surface p-3 transition hover:border-line-strong hover:bg-raised"
		>
			<Cover src={l.image} title={l.name} type="movie" class="aspect-[2/3] w-12 shrink-0" />
			<span class="min-w-0 flex-1">
				<span class="block truncate text-[1rem] font-semibold">{l.name}</span>
				<span class="mt-0.5 flex items-center gap-1.5 text-[0.8125rem] text-muted">
					{l.items_count} {l.items_count === 1 ? 'item' : 'items'}
					<span aria-hidden="true">·</span>
					{#if l.visibility === 'public'}<Globe class="size-3.5" /><span class="sr-only">Public</span>{:else}<Lock class="size-3.5" /><span class="sr-only">Private</span>{/if}
					{#if !l.is_owner}<span aria-hidden="true">·</span>by {l.owner.username}{/if}
				</span>
				{#if l.description}<span class="mt-0.5 line-clamp-1 block text-[0.75rem] text-faint">{l.description}</span>{/if}
			</span>
		</a>
	</li>
{/snippet}

<svelte:head><title>Lists · Encompass</title></svelte:head>

<PullToRefresh onrefresh={load}>
	<header class="mb-3 flex items-center justify-between gap-3">
		<h1 class="text-3xl font-semibold tracking-tight">Lists</h1>
		<Button variant="primary" icon={ListPlus} onclick={() => (createOpen = true)}>New list</Button>
	</header>

	<input type="search" class="input" placeholder="Search lists" aria-label="Search lists" bind:value={query} autocomplete="off" />
	<ChipRow label="Sort lists" sticky={false}>
		{#each SORTS as s (s.key)}
			<Chip selected={sort === s.key} onclick={() => (sort = s.key)}>{s.label}</Chip>
		{/each}
	</ChipRow>

	{#if lists === null}
		<div class="space-y-3" aria-hidden="true"><Skeleton class="h-20 w-full" /><Skeleton class="h-20 w-full" /><Skeleton class="h-20 w-full" /></div>
	{:else if lists.length === 0}
		<EmptyState icon={ListPlus} title={query ? 'No lists match' : 'No lists yet'} description={query ? undefined : 'Collect titles into lists like "Rewatch" or "Book club picks".'}>
			{#snippet actions()}{#if !query}<Button variant="primary" onclick={() => (createOpen = true)}>Create a list</Button>{/if}{/snippet}
		</EmptyState>
	{:else}
		{#if owned.length}
			<ul class="grid grid-cols-1 gap-2 md:grid-cols-2 xl:grid-cols-3" aria-label="Your lists">
				{#each owned as l (l.id)}{@render card(l)}{/each}
			</ul>
		{/if}
		{#if shared.length}
			<h2 class="mt-8 mb-2 flex items-center gap-1.5 px-1 text-[0.8125rem] font-semibold tracking-wide text-faint uppercase"><Users class="size-3.5" />Shared with you</h2>
			<ul class="grid grid-cols-1 gap-2 md:grid-cols-2 xl:grid-cols-3" aria-label="Shared lists">
				{#each shared as l (l.id)}{@render card(l)}{/each}
			</ul>
		{/if}
	{/if}
</PullToRefresh>

<ListFormSheet bind:open={createOpen} saving={creating} onsave={create} />
