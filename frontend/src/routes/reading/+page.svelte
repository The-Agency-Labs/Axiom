<script lang="ts">
	import { get, qs } from '#lib/api.ts';
	import CurrentlyReading from '#lib/components/reading/CurrentlyReading.svelte';
	import DailyLog from '#lib/components/reading/DailyLog.svelte';
	import { isoDaysAgo } from '#lib/components/reading/format.ts';
	import LinkBooks from '#lib/components/reading/LinkBooks.svelte';
	import type { KindleDocument } from '#lib/components/reading/types.ts';
	import ReadingStatsCard from '#lib/components/reading/ReadingStats.svelte';
	import TodayCard from '#lib/components/reading/TodayCard.svelte';
	import type { ReadingStats } from '#lib/types.ts';
	import { Button, PullToRefresh, Skeleton, Tabs } from '#lib/ui/index.ts';

	let stats = $state<ReadingStats | null>(null);
	let version = $state(0);
	let tab = $state('log');
	let unlinked = $state(0);

	async function load() {
		const [s, docs] = await Promise.all([
			get<ReadingStats>('/reading/stats' + qs({ start: isoDaysAgo(6) }), (d) => (stats = d)),
			get<KindleDocument[]>('/reading/documents' + qs({ unlinked: true, suggestions: false })).catch(() => [])
		]);
		stats = s;
		unlinked = docs.length;
	}
	$effect(() => {
		load().catch(() => {});
	});

	async function refresh() {
		version++;
		await load().catch(() => {});
	}
</script>

<svelte:head><title>Reading · Axiom</title></svelte:head>

<PullToRefresh onrefresh={refresh}>
	<header class="mb-4 flex items-end justify-between gap-3">
		<h1 class="text-3xl font-semibold tracking-tight">Reading</h1>
		<Button href="/series" size="sm" variant="secondary">Series</Button>
	</header>

	<div class="grid grid-cols-1 gap-5 lg:grid-cols-[minmax(0,2fr)_minmax(0,3fr)] lg:items-start">
		<div class="space-y-4">
			{#if stats}<TodayCard {stats} />{:else}<Skeleton class="h-48 w-full" />{/if}
			<CurrentlyReading {version} />
		</div>

		<div class="min-w-0">
			<Tabs
				label="Reading sections"
				variant="underline"
				bind:value={tab}
				class="mb-4"
				options={[
					{ value: 'log', label: 'Daily log' },
					{ value: 'link', label: 'Link books', count: unlinked || undefined },
					{ value: 'stats', label: 'Stats' }
				]}
			/>
			{#if tab === 'log'}
				<DailyLog {version} />
			{:else if tab === 'link'}
				<LinkBooks onchanged={() => (version++, load())} />
			{:else}
				<ReadingStatsCard />
			{/if}
		</div>
	</div>
</PullToRefresh>
