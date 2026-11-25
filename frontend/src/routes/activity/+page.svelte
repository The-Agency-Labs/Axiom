<script lang="ts">
	import { CalendarDays, LoaderCircle } from '@lucide/svelte';
	import { get, qs } from '#lib/api.ts';
	import Cover from '#lib/components/Cover.svelte';
	import { MEDIA } from '#lib/media.ts';
	import type { ActivityEntry } from '#lib/types.ts';

	let entries = $state<ActivityEntry[]>([]);
	let cursor = $state<string | null>(null);
	let loading = $state(false);
	let done = $state(false);

	async function more() {
		if (loading || done) return;
		loading = true;
		try {
			const data = await get<{ entries: ActivityEntry[]; next_cursor: string | null }>(
				'/activity' + qs({ limit: 40, cursor })
			);
			entries = [...entries, ...data.entries];
			cursor = data.next_cursor;
			done = !data.next_cursor;
		} finally {
			loading = false;
		}
	}

	$effect(() => {
		more();
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
		const observer = new IntersectionObserver((items) => items[0].isIntersecting && more(), {
			rootMargin: '400px'
		});
		observer.observe(node);
		return { destroy: () => observer.disconnect() };
	}
</script>

<svelte:head><title>Activity · Encompass</title></svelte:head>

<header class="mb-8 flex flex-wrap items-end justify-between gap-4">
	<div>
		<h1 class="text-3xl font-semibold tracking-tight">Activity</h1>
		<p class="mt-1 text-sm text-muted">Everything you've tracked, newest first.</p>
	</div>
	<a
		href="/year"
		class="flex items-center gap-2 rounded-full border border-line bg-surface px-4 py-2 text-sm font-medium transition hover:border-line-strong hover:bg-raised"
	>
		<CalendarDays class="size-4 text-brand" /> {new Date().getFullYear()} in review
	</a>
</header>

<div class="max-w-3xl space-y-8">
	{#each days as [day, items] (day)}
		<section>
			<h2 class="sticky top-0 z-10 -mx-1 mb-2 bg-canvas/90 px-1 py-2 text-xs font-semibold tracking-wider text-faint uppercase backdrop-blur">
				{dayLabel(day)}
			</h2>
			<ol class="space-y-1">
				{#each items as entry, i (i)}
					{@const meta = MEDIA[entry.media_type]}
					<li class="flex items-center gap-3 rounded-xl px-2 py-2 transition hover:bg-ink/[0.03]">
						<Cover src={entry.item.image} title={entry.item.title} type={entry.media_type} class="aspect-[2/3] w-9 shrink-0 rounded-md" />
						<div class="min-w-0 flex-1">
							<p class="truncate text-sm font-medium">{entry.item.title}</p>
							<p class="line-clamp-1 text-xs text-muted">
								{entry.changes.map((c) => c.description).join(' · ')}
							</p>
						</div>
						<div class="flex shrink-0 flex-col items-end gap-1">
							<meta.icon class="size-3.5" style="color: {meta.accent}" />
							<span class="text-[11px] text-faint tabular-nums">
								{new Date(entry.date).toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' })}
							</span>
						</div>
					</li>
				{/each}
			</ol>
		</section>
	{/each}

	{#if !done}
		<div use:sentinel class="flex justify-center py-6 text-faint">
			{#if loading}<LoaderCircle class="size-5 animate-spin" />{/if}
		</div>
	{:else if entries.length === 0}
		<p class="text-sm text-muted">No activity yet.</p>
	{/if}
</div>
