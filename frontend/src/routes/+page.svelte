<script lang="ts">
	import { CheckCheck, Plus } from '@lucide/svelte';
	import { get } from '#lib/api.ts';
	import { auth } from '#lib/auth.svelte.ts';
	import Cover from '#lib/components/Cover.svelte';
	import NowCard from '#lib/components/NowCard.svelte';
	import ReadingStrip from '#lib/components/ReadingStrip.svelte';
	import SectionHeader from '#lib/components/SectionHeader.svelte';
	import { MEDIA, relativeTime } from '#lib/media.ts';
	import type { Finished, NowGroup, ReadingStats } from '#lib/types.ts';

	let groups = $state<NowGroup[] | null>(null);
	let finished = $state<Finished[]>([]);
	let reading = $state<ReadingStats | null>(null);
	let error = $state('');

	async function load() {
		try {
			const [now, done, stats] = await Promise.all([
				get<{ groups: NowGroup[] }>('/now'),
				get<Finished[]>('/now/finished?limit=14'),
				get<ReadingStats>('/reading/stats?' + new URLSearchParams({ start: daysAgo(6) }))
			]);
			groups = now.groups;
			finished = done;
			reading = stats;
		} catch (e) {
			error = 'Could not load your media.';
		}
	}

	function daysAgo(n: number) {
		const d = new Date();
		d.setDate(d.getDate() - n);
		return d.toLocaleDateString('en-CA');
	}

	const greeting = $derived.by(() => {
		const hour = new Date().getHours();
		if (hour < 5) return 'Up late';
		if (hour < 12) return 'Good morning';
		if (hour < 18) return 'Good afternoon';
		return 'Good evening';
	});
	const totalInProgress = $derived(groups?.reduce((n, g) => n + g.entries.length, 0) ?? 0);
	const today = new Date().toLocaleDateString(undefined, { weekday: 'long', month: 'long', day: 'numeric' });

	$effect(() => {
		load();
	});
</script>

<svelte:head><title>Now · Encompass</title></svelte:head>

<header class="mb-8 flex flex-wrap items-end justify-between gap-4">
	<div>
		<p class="text-sm text-muted">{today}</p>
		<h1 class="mt-1 text-3xl font-semibold tracking-tight sm:text-4xl">
			{greeting}, <span class="text-muted capitalize">{auth.username}</span>
		</h1>
		{#if groups}
			<p class="mt-2 text-sm text-faint">
				{totalInProgress} in progress across {groups.length} {groups.length === 1 ? 'section' : 'sections'}
			</p>
		{/if}
	</div>
	<a
		href="/search"
		class="flex items-center gap-2 rounded-full border border-line bg-surface px-4 py-2 text-sm font-medium transition hover:border-line-strong hover:bg-raised"
	>
		<Plus class="size-4" /> Track something
	</a>
</header>

{#if error}
	<p class="rounded-xl border border-rose-500/30 bg-rose-500/10 p-4 text-sm text-rose-300">{error}</p>
{:else if !groups}
	<div class="space-y-10">
		{#each [0, 1, 2] as i (i)}
			<div>
				<div class="mb-3 h-6 w-32 animate-pulse rounded bg-ink/5"></div>
				<div class="flex gap-4 overflow-hidden">
					{#each [0, 1, 2] as j (j)}
						<div class="h-40 w-[22rem] shrink-0 animate-pulse rounded-2xl bg-ink/[0.03]"></div>
					{/each}
				</div>
			</div>
		{/each}
	</div>
{:else}
	{#if reading}
		<section class="mb-10">
			<SectionHeader title="Reading" icon={MEDIA.book.icon} accent={MEDIA.book.accent} href="/reading" />
			<ReadingStrip stats={reading} />
		</section>
	{/if}

	<div class="mb-4 flex items-center gap-3">
		<h2 class="text-xs font-semibold tracking-wider text-faint uppercase">Media Now</h2>
		<div class="h-px flex-1 bg-line"></div>
	</div>

	{#if groups.length === 0}
		<div class="rounded-2xl border border-dashed border-line-strong p-10 text-center">
			<p class="font-medium">Nothing in progress</p>
			<p class="mt-1 text-sm text-muted">Start a show, a book or some manga and it will show up here.</p>
		</div>
	{/if}

	<div class="space-y-10">
		{#each groups as group (group.media_type)}
			{@const meta = MEDIA[group.media_type]}
			<section>
				<SectionHeader
					title={meta.plural}
					icon={meta.icon}
					accent={meta.accent}
					count={group.entries.length}
					href="/library/{group.media_type}"
				/>
				<div class="shelf -mx-4 flex gap-4 overflow-x-auto px-4 pb-2 sm:-mx-6 sm:px-6 lg:-mx-10 lg:px-10">
					{#each group.entries as entry (entry.media_type + entry.id)}
						<NowCard {entry} />
					{/each}
				</div>
			</section>
		{/each}
	</div>

	{#if finished.length}
		<section class="mt-12">
			<SectionHeader title="Recently finished" icon={CheckCheck} accent="#34d399" />
			<div class="shelf -mx-4 flex gap-3 overflow-x-auto px-4 pb-2 sm:-mx-6 sm:px-6 lg:-mx-10 lg:px-10">
				{#each finished as entry (entry.media_type + entry.id)}
					<a href="/item/{entry.media_type}/{entry.id}" class="group w-28 shrink-0">
						<Cover
							src={entry.item.image}
							title={entry.item.title}
							type={entry.media_type}
							class="aspect-[2/3] w-full transition group-hover:ring-line-strong"
						/>
						<p class="mt-2 line-clamp-1 text-xs font-medium">{entry.item.title}</p>
						<p class="text-[11px] text-faint">
							{relativeTime(entry.end_date)}{entry.score ? ` · ★ ${entry.score}` : ''}
						</p>
					</a>
				{/each}
			</div>
		</section>
	{/if}
{/if}
