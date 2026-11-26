<script lang="ts">
	import { ArrowDown, ArrowLeft, ArrowUp, GripVertical, Globe, LayoutGrid, List, ListPlus, Lock, Pencil, Users, X } from '@lucide/svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { get, mutate, qs } from '#lib/api.ts';
	import Cover from '#lib/components/Cover.svelte';
	import ListFormSheet from '#lib/components/reading/ListFormSheet.svelte';
	import ListMembersSheet from '#lib/components/reading/ListMembersSheet.svelte';
	import type { ListDetail, ListItem } from '#lib/components/reading/types.ts';
	import { MEDIA } from '#lib/media.ts';
	import { Button, Chip, ChipRow, EmptyState, IconButton, PosterCard, PosterGrid, Skeleton, StatusBadge, Tabs } from '#lib/ui/index.ts';

	let list = $state<ListDetail | null>(null);
	let error = $state('');
	let sort = $state('date_added');
	let type = $state('all');
	let view = $state('grid');
	let q = $state('');
	let editOpen = $state(false);
	let membersOpen = $state(false);
	let saving = $state(false);

	const id = $derived(page.params.id);
	const manual = $derived(sort === 'position');
	const asList = $derived(view === 'list' || manual);

	async function load() {
		const current = id;
		try {
			const d = await get<ListDetail>(
				`/lists/${current}` + qs({ sort, type: manual ? '' : type, q: manual ? '' : q.trim(), page_size: 500 }),
				(f) => (list = f)
			);
			if (current === id) list = d;
		} catch {
			error = 'This list is not available.';
		}
	}
	$effect(() => {
		void id;
		void sort;
		void type;
		const t = setTimeout(load, q ? 250 : 0);
		void q;
		return () => clearTimeout(t);
	});

	const href = (i: ListItem) =>
		i.entry ? `/item/${i.item.media_type}/${i.entry.id}` : `/media/${i.item.media_type}/${i.item.source}/${i.item.media_id}`;

	async function saveMeta(v: { name: string; description: string; visibility: 'private' | 'public' }) {
		if (!list) return;
		saving = true;
		const body: Record<string, string> = { name: v.name, description: v.description };
		if (list.is_owner) body.visibility = v.visibility;
		const res = await mutate<ListDetail>({ method: 'PATCH', path: `/lists/${list.id}`, body, label: 'List saved' });
		saving = false;
		if (res.ok && res.data) {
			list.name = res.data.name;
			list.description = res.data.description;
			list.visibility = res.data.visibility;
			editOpen = false;
		}
	}
	async function remove() {
		if (!list) return;
		const res = await mutate({ method: 'DELETE', path: `/lists/${list.id}`, label: 'List deleted' });
		if (res.ok) goto('/lists', { replaceState: true });
	}
	async function toggleVisibility() {
		if (!list?.is_owner) return;
		const next = list.visibility === 'public' ? 'private' : 'public';
		const l = list;
		await mutate({
			method: 'PATCH',
			path: `/lists/${l.id}`,
			body: { visibility: next },
			label: `List is now ${next}`,
			optimistic: () => {
				const before = l.visibility;
				l.visibility = next;
				return () => (l.visibility = before);
			}
		});
	}

	function removeItem(i: ListItem) {
		const l = list!;
		return mutate({
			method: 'DELETE',
			path: `/lists/${l.id}/items/${i.item.id}`,
			label: `Removed ${i.item.title}`,
			optimistic: () => {
				const before = l.items;
				l.items = l.items.filter((x) => x !== i);
				l.items_count -= 1;
				return () => ((l.items = before), (l.items_count += 1));
			}
		});
	}

	// --- manual order: move up/down everywhere, drag on desktop ---
	function persistOrder(before: ListItem[]) {
		const l = list!;
		return mutate({
			method: 'PUT',
			path: `/lists/${l.id}/order`,
			body: { item_ids: l.items.map((x) => x.item.id) },
			label: 'Order saved',
			toastOnError: true
		}).then((r) => {
			if (!r.ok) l.items = before;
		});
	}
	function move(from: number, to: number) {
		const l = list!;
		if (to < 0 || to >= l.items.length || from === to) return;
		const before = l.items;
		const next = [...l.items];
		next.splice(to, 0, next.splice(from, 1)[0]);
		l.items = next;
		persistOrder(before);
	}
	let dragFrom = $state<number | null>(null);
	let dragOver = $state<number | null>(null);
</script>

<svelte:head><title>{list?.name ?? 'List'} · Encompass</title></svelte:head>

<div class="mb-2 flex items-center gap-1">
	<IconButton icon={ArrowLeft} label="Back to lists" href="/lists" />
	<h1 class="min-w-0 flex-1 truncate text-2xl font-semibold tracking-tight">{list?.name ?? ''}</h1>
	{#if list?.can_edit}
		<IconButton icon={Pencil} label="Edit list" onclick={() => (editOpen = true)} />
	{/if}
</div>

{#if error}
	<EmptyState icon={ListPlus} title="List not found" description={error}>
		{#snippet actions()}<Button href="/lists">Back to lists</Button>{/snippet}
	</EmptyState>
{:else if !list}
	<div class="space-y-3" aria-hidden="true"><Skeleton class="h-10 w-2/3" /><Skeleton class="h-64 w-full" /></div>
{:else}
	{#if list.description}<p class="mb-3 px-1 text-[0.875rem] text-muted">{list.description}</p>{/if}
	<div class="mb-3 flex flex-wrap items-center gap-2 px-1 text-[0.8125rem] text-muted">
		<span>{list.items_count} {list.items_count === 1 ? 'item' : 'items'}</span>
		<span aria-hidden="true">·</span>
		<span>by {list.owner.username}</span>
		<button
			type="button"
			class="inline-flex min-h-11 items-center gap-1 rounded-full px-2 {list.is_owner ? 'hover:bg-ink/6' : 'pointer-events-none'}"
			onclick={toggleVisibility}
			aria-label={list.is_owner ? `Visibility: ${list.visibility}. Tap to change` : `Visibility: ${list.visibility}`}
		>
			{#if list.visibility === 'public'}<Globe class="size-3.5" />Public{:else}<Lock class="size-3.5" />Private{/if}
		</button>
		<button type="button" class="inline-flex min-h-11 items-center gap-1 rounded-full px-2 hover:bg-ink/6" onclick={() => (membersOpen = true)}>
			<Users class="size-3.5" />{list.members.length ? `${list.members.length} ${list.members.length === 1 ? 'member' : 'members'}` : 'Members'}
		</button>
	</div>

	<div class="flex flex-wrap items-center gap-2">
		<Tabs
			label="Sort"
			bind:value={sort}
			options={[
				{ value: 'date_added', label: 'Added' },
				{ value: 'title', label: 'Title' },
				{ value: 'media_type', label: 'Type' },
				{ value: 'position', label: 'Manual', disabled: !list.can_edit }
			]}
		/>
		{#if !manual}<Tabs
			label="Layout"
			bind:value={view}
			options={[
				{ value: 'grid', label: 'Grid' },
				{ value: 'list', label: 'List' }
			]}
		/>{/if}
	</div>

	{#if !manual}
		<input type="search" class="input mt-3" placeholder="Filter by title" aria-label="Filter by title" bind:value={q} autocomplete="off" />
		{#if list.media_types.length > 1}
			<ChipRow label="Filter by type" sticky={false}>
				<Chip selected={type === 'all'} onclick={() => (type = 'all')}>All</Chip>
				{#each list.media_types as t (t)}
					<Chip selected={type === t} color={MEDIA[t]?.accent} onclick={() => (type = t)}>{MEDIA[t]?.plural ?? t}</Chip>
				{/each}
			</ChipRow>
		{/if}
	{:else}
		<p class="mt-3 px-1 text-[0.8125rem] text-muted">Use the arrows to reorder, or drag a row on a computer.</p>
	{/if}

	<div class="mt-4">
		{#if list.items.length === 0}
			<EmptyState icon={ListPlus} title={q || type !== 'all' ? 'Nothing matches' : 'This list is empty'} description={q || type !== 'all' ? undefined : 'Add titles from any title page using "Add to list".'} />
		{:else if !asList}
			<PosterGrid>
				{#each list.items as i (i.item.id)}
					<PosterCard href={href(i)} src={i.item.image} title={i.item.title} type={i.item.media_type} subtitle={MEDIA[i.item.media_type].label} status={i.entry?.status} score={i.entry?.score} />
				{/each}
			</PosterGrid>
		{:else}
			<ul class="divide-y divide-line overflow-hidden rounded-[var(--radius-card)] border border-line bg-surface">
				{#each list.items as i, n (i.item.id)}
					<li
						class="flex items-center gap-2 py-1.5 pr-1 pl-3 {dragOver === n && dragFrom !== null && dragFrom !== n ? 'bg-brand/10' : ''}"
						draggable={manual}
						ondragstart={() => (dragFrom = n)}
						ondragover={(e) => {
							if (dragFrom === null) return;
							e.preventDefault();
							dragOver = n;
						}}
						ondrop={(e) => {
							e.preventDefault();
							if (dragFrom !== null) move(dragFrom, n);
							dragFrom = dragOver = null;
						}}
						ondragend={() => (dragFrom = dragOver = null)}
					>
						{#if manual}<GripVertical class="hidden size-4 shrink-0 cursor-grab text-faint [@media(hover:hover)]:block" aria-hidden="true" />{/if}
						<a href={href(i)} class="flex min-w-0 flex-1 items-center gap-3 py-1">
							<Cover src={i.item.image} title={i.item.title} type={i.item.media_type} class="aspect-[2/3] w-10 shrink-0 !rounded-md" />
							<span class="min-w-0">
								<span class="line-clamp-2 text-[0.875rem] leading-snug font-medium">{i.item.title}</span>
								<span class="flex items-center gap-2 text-[0.75rem] text-faint">
									{MEDIA[i.item.media_type].label}
									{#if i.entry}<StatusBadge status={i.entry.status} compact />{/if}
								</span>
							</span>
						</a>
						{#if manual}
							<IconButton icon={ArrowUp} label="Move {i.item.title} up" disabled={n === 0} onclick={() => move(n, n - 1)} />
							<IconButton icon={ArrowDown} label="Move {i.item.title} down" disabled={n === list.items.length - 1} onclick={() => move(n, n + 1)} />
						{/if}
						{#if list.can_edit}
							<IconButton icon={X} label="Remove {i.item.title} from list" onclick={() => removeItem(i)} />
						{/if}
					</li>
				{/each}
			</ul>
		{/if}
	</div>

	<ListFormSheet
		bind:open={editOpen}
		initial={{ name: list.name, description: list.description, visibility: list.visibility }}
		canChangeVisibility={list.is_owner}
		{saving}
		onsave={saveMeta}
		ondelete={list.can_delete ? remove : undefined}
	/>
	<ListMembersSheet bind:open={membersOpen} listId={list.id} owner={list.owner.username} bind:members={list.members} canEdit={list.can_edit} />
{/if}
