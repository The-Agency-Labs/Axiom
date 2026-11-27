<script lang="ts">
	import { CheckCheck, Plus, Sparkles } from '@lucide/svelte';
	import { get } from '#lib/api.ts';
	import NowItem from '#lib/components/library/NowItem.svelte';
	import SectionHead from '#lib/components/library/SectionHead.svelte';
	import { MEDIA, relativeTime, titleHref } from '#lib/media.ts';
	import { onSynced } from '#lib/offline.svelte.ts';
	import type { Finished, NowGroup } from '#lib/types.ts';
	import { Button, EmptyState, PosterCard, PullToRefresh, Skeleton, Shelf } from '#lib/ui/index.ts';

	let groups = $state<NowGroup[] | null>(null);
	let finished = $state<Finished[]>([]);
	let error = $state(false);

	// The server orders sections by what you touched last.
	const order = (list: NowGroup[]) => list;

	async function load() {
		error = false;
		try {
			const [now, done] = await Promise.all([
				get<{ groups: NowGroup[] }>('/now', (d) => (groups = order(d.groups))),
				get<Finished[]>('/now/finished?limit=14', (d) => (finished = d))
			]);
			groups = order(now.groups);
			finished = done;
		} catch {
			if (!groups) error = true;
		}
	}

	/** Refetch quietly after a tap (no skeleton): the optimistic state is already on screen. */
	async function quiet() {
		try {
			groups = order((await get<{ groups: NowGroup[] }>('/now')).groups);
		} catch {
			/* keep what is shown */
		}
	}

	const total = $derived(groups?.reduce((n, g) => n + g.entries.length, 0) ?? 0);

	$effect(() => {
		load();
		return onSynced(quiet);
	});
</script>

<svelte:head><title>Now · Axiom</title></svelte:head>

<PullToRefresh onrefresh={load}>
	<header class="mb-5 flex items-end justify-between gap-3">
		<div class="min-w-0">
			<h1 class="truncate text-2xl font-semibold tracking-tight sm:text-3xl">Now</h1>
			<p class="mt-1 h-5 text-[0.8125rem] text-faint">
				{#if groups}{total} in progress{/if}
			</p>
		</div>
		<Button href="/search" variant="secondary" icon={Plus} class="shrink-0">Track</Button>
	</header>

	{#if error}
		<EmptyState
			title="Could not load your media"
			description="Check your connection and pull down to try again."
		>
			{#snippet actions()}<Button variant="primary" onclick={load}>Retry</Button>{/snippet}
		</EmptyState>
	{:else if !groups}
		<div class="space-y-8" aria-busy="true">
			{#each [0, 1] as i (i)}
				<div>
					<Skeleton class="mb-3 h-7 w-40" />
					<div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
						<Skeleton class="h-[10.5rem]" />
						<Skeleton class="hidden h-[10.5rem] sm:block" />
					</div>
				</div>
			{/each}
		</div>
	{:else}
		{#if groups.length === 0}
			<EmptyState
				icon={Sparkles}
				title="Nothing in progress"
				description="Start a show, a book or some manga and it will show up here."
			>
				{#snippet actions()}<Button variant="primary" href="/search" icon={Plus}>Find something</Button>{/snippet}
			</EmptyState>
		{/if}

		<div class="space-y-8">
			{#each groups as group (group.media_type)}
				{@const meta = MEDIA[group.media_type]}
				<section>
					<SectionHead
						title={meta.plural}
						icon={meta.icon}
						accent={meta.accent}
						count={group.entries.length}
						href="/library/{group.media_type}"
					/>
					<div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
						{#each group.entries as entry (entry.media_type + entry.id + ':' + (entry.season_number ?? ''))}
							<NowItem {entry} kind={group.media_type} onchange={quiet} />
						{/each}
					</div>
				</section>
			{/each}
		</div>


		{#if finished.length}
			<section class="mt-10">
				<Shelf label="recently finished" class="-mx-4 gap-3 px-4 pb-2 sm:-mx-6 sm:px-6 lg:-mx-10 lg:px-10 scroll-px-4 sm:scroll-px-6 lg:scroll-px-10">
					{#snippet header()}<SectionHead title="Recently finished" icon={CheckCheck} accent="var(--color-success)" />{/snippet}
					{#each finished as entry (entry.media_type + entry.id)}
						<div class="w-28 shrink-0">
							<PosterCard
								href={titleHref(entry.item, entry.media_type, entry.id)}
								src={entry.item.image}
								title={entry.item.title}
								type={entry.media_type}
								score={entry.score}
								subtitle={relativeTime(entry.end_date)}
							/>
						</div>
					{/each}
				</Shelf>
			</section>
		{/if}
	{/if}
</PullToRefresh>
