<script lang="ts">
	import { CalendarDays, History } from '@lucide/svelte';
	import { get, qs } from '#lib/api.ts';
	import Cover from '#lib/components/Cover.svelte';
	import { MEDIA, titleHref } from '#lib/media.ts';
	import type { ActivityEntry } from '#lib/types.ts';
	import { Button, EmptyState, ListRow, PullToRefresh, Skeleton } from '#lib/ui/index.ts';

	let entries = $state<ActivityEntry[]>([]);
	let cursor = $state<string | null>(null);
	let loading = $state(false);
	let done = $state(false);
	let failed = $state(false);
	let generation = 0;

	async function more() {
		if (loading || done) return;
		const mine = generation;
		loading = true;
		failed = false;
		try {
			const data = await get<{ entries: ActivityEntry[]; next_cursor: string | null }>(
				'/activity' + qs({ limit: 40, cursor })
			);
			if (mine !== generation) return;
			entries = [...entries, ...data.entries];
			cursor = data.next_cursor;
			done = !data.next_cursor;
		} catch {
			if (mine === generation) failed = true;
		} finally {
			if (mine === generation) loading = false;
		}
	}

	async function reset() {
		generation++;
		entries = [];
		cursor = null;
		done = false;
		loading = false;
		await more();
	}

	$effect(() => {
		void more();
	});

	const days = $derived.by(() => {
		const map = new Map<string, ActivityEntry[]>();
		for (const e of entries) {
			const day = new Date(e.date).toLocaleDateString('en-CA');
			map.set(day, [...(map.get(day) ?? []), e]);
		}
		return [...map.entries()];
	});

	function dayLabel(iso: string) {
		const today = new Date().toLocaleDateString('en-CA');
		const yesterday = new Date(Date.now() - 864e5).toLocaleDateString('en-CA');
		if (iso === today) return 'Today';
		if (iso === yesterday) return 'Yesterday';
		return new Date(iso + 'T12:00:00').toLocaleDateString(undefined, { weekday: 'long', month: 'short', day: 'numeric' });
	}

	function sentinel(node: HTMLElement) {
		const observer = new IntersectionObserver((items) => items[0].isIntersecting && more(), { rootMargin: '500px' });
		observer.observe(node);
		return { destroy: () => observer.disconnect() };
	}

	const timeOf = (iso: string) => new Date(iso).toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' });
</script>

<svelte:head><title>Activity · Encompass</title></svelte:head>

<PullToRefresh onrefresh={reset}>
	<header class="mb-3 flex items-center justify-between gap-3">
		<div>
			<h1 class="text-2xl font-semibold tracking-tight sm:text-3xl">Activity</h1>
			<p class="text-[13px] text-faint">Everything you tracked, newest first</p>
		</div>
		<Button href="/year" variant="secondary" icon={CalendarDays} class="shrink-0">{new Date().getFullYear()}</Button>
	</header>

	<div class="max-w-3xl">
		{#each days as [day, items] (day)}
			<section class="mb-5">
				<h2
					class="sticky top-[env(safe-area-inset-top)] z-10 -mx-4 bg-canvas/90 px-4 py-2 text-xs font-semibold tracking-wider text-faint uppercase backdrop-blur-xl sm:-mx-6 sm:px-6 lg:mx-0 lg:px-0"
				>
					{dayLabel(day)}
				</h2>
				<ol class="divide-y divide-line overflow-hidden rounded-[var(--radius-card)] border border-line bg-surface">
					{#each items as entry, i (day + i + entry.date)}
						{@const meta = MEDIA[entry.media_type] ?? MEDIA.tv}
						<li>
							<ListRow
								href={titleHref(entry.item, entry.media_type)}
								title={entry.item.display_title ?? entry.item.title}
								subtitle={entry.changes.map((c) => c.description).join(' · ')}
							>
								{#snippet leading()}
									<Cover src={entry.item.image} title={entry.item.title} type={entry.media_type} class="aspect-[2/3] w-10 shrink-0 rounded-md" />
								{/snippet}
								{#snippet trailing()}
									<span class="flex shrink-0 flex-col items-end gap-1">
										<meta.icon class="size-3.5" style="color: {meta.accent}" />
										<span class="text-[11px] text-faint tabular-nums">{timeOf(entry.date)}</span>
									</span>
								{/snippet}
							</ListRow>
						</li>
					{/each}
				</ol>
			</section>
		{/each}

		{#if entries.length === 0 && loading}
			<div class="space-y-2" aria-busy="true">
				{#each Array(8) as _, i (i)}<Skeleton class="h-16 w-full" />{/each}
			</div>
		{:else if failed}
			<EmptyState title="Could not load activity" description="Check your connection and try again.">
				{#snippet actions()}<Button variant="primary" onclick={more}>Retry</Button>{/snippet}
			</EmptyState>
		{:else if done && entries.length === 0}
			<EmptyState icon={History} title="No activity yet" description="Mark an episode or log some pages and it will appear here.">
				{#snippet actions()}<Button variant="primary" href="/search">Find something</Button>{/snippet}
			</EmptyState>
		{:else if !done}
			<div use:sentinel class="h-20 py-2">
				{#if loading}<Skeleton class="h-16 w-full" />{/if}
			</div>
		{/if}
	</div>
</PullToRefresh>
