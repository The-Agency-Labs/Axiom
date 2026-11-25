<script lang="ts">
	import { Search, Star } from '@lucide/svelte';
	import { page } from '$app/state';
	import { get, qs } from '#lib/api.ts';
	import Cover from '#lib/components/Cover.svelte';
	import { isMediaType, MEDIA, progressLabel, STATUS_COLOR, STATUSES } from '#lib/media.ts';
	import type { Entry, MediaType, Status } from '#lib/types.ts';

	const type = $derived(page.params.type as MediaType);
	const meta = $derived(isMediaType(type) ? MEDIA[type] : MEDIA.tv);

	let entries = $state<Entry[] | null>(null);
	let tab = $state<Status | 'All'>('In progress');
	let filter = $state('');

	$effect(() => {
		const current = type;
		entries = null;
		get<{ results: Entry[] }>('/library' + qs({ media_type: current, limit: 1000 })).then((data) => {
			if (current !== type) return;
			entries = data.results;
			// Open on the first non-empty tab.
			if (!data.results.some((e) => e.status === tab)) {
				tab = STATUSES.find((s) => data.results.some((e) => e.status === s)) ?? 'All';
			}
		});
	});

	const counts = $derived.by(() => {
		const map: Record<string, number> = { All: entries?.length ?? 0 };
		for (const e of entries ?? []) map[e.status] = (map[e.status] ?? 0) + 1;
		return map;
	});

	const visible = $derived(
		(entries ?? []).filter(
			(e) =>
				(tab === 'All' || e.status === tab) &&
				(!filter || e.item.title.toLowerCase().includes(filter.toLowerCase()))
		)
	);
</script>

<svelte:head><title>{meta.plural} · Encompass</title></svelte:head>

<header class="mb-6 flex flex-wrap items-center justify-between gap-4">
	<div class="flex items-center gap-3">
		<span
			class="grid size-10 place-items-center rounded-xl"
			style="background: color-mix(in oklab, {meta.accent} 16%, transparent); color: {meta.accent}"
		>
			<meta.icon class="size-5" />
		</span>
		<div>
			<h1 class="text-2xl font-semibold tracking-tight sm:text-3xl">{meta.plural}</h1>
			<p class="text-sm text-faint">{counts.All} tracked</p>
		</div>
	</div>
	<label class="relative w-full sm:w-64">
		<Search class="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-faint" />
		<input
			bind:value={filter}
			placeholder="Filter {meta.plural.toLowerCase()}…"
			class="w-full rounded-xl border border-line bg-surface py-2 pr-3 pl-9 text-sm outline-none transition focus:border-line-strong"
		/>
	</label>
</header>

<div class="shelf -mx-4 mb-6 flex gap-1.5 overflow-x-auto px-4 sm:mx-0 sm:px-0">
	{#each ['All', ...STATUSES] as status (status)}
		<button
			onclick={() => (tab = status as Status | 'All')}
			class="flex shrink-0 items-center gap-2 rounded-full border px-3.5 py-1.5 text-sm font-medium transition {tab === status
				? 'border-line-strong bg-white/8 text-ink'
				: 'border-transparent text-muted hover:text-ink'}"
		>
			{#if status !== 'All'}
				<span class="size-1.5 rounded-full" style="background: {STATUS_COLOR[status as Status]}"></span>
			{/if}
			{status}
			<span class="text-xs text-faint tabular-nums">{counts[status] ?? 0}</span>
		</button>
	{/each}
</div>

{#if !entries}
	<div class="grid grid-cols-2 gap-x-4 gap-y-6 sm:grid-cols-4 md:grid-cols-5 xl:grid-cols-7">
		{#each Array(10) as _, i (i)}
			<div class="aspect-[2/3] animate-pulse rounded-xl bg-white/[0.04]"></div>
		{/each}
	</div>
{:else if visible.length === 0}
	<div class="rounded-2xl border border-dashed border-line-strong p-10 text-center text-sm text-muted">
		Nothing here yet. <a href="/search?type={type}" class="text-ink underline underline-offset-4">Find something to track</a>.
	</div>
{:else}
	<div class="grid grid-cols-2 gap-x-4 gap-y-6 sm:grid-cols-4 md:grid-cols-5 xl:grid-cols-7">
		{#each visible as entry (entry.id)}
			<a href="/item/{type}/{entry.id}" class="group">
				<div class="relative">
					<Cover
						src={entry.item.image}
						title={entry.item.title}
						type={entry.media_type}
						class="aspect-[2/3] w-full transition duration-300 group-hover:-translate-y-0.5 group-hover:ring-line-strong"
					/>
					{#if entry.score}
						<span
							class="absolute top-2 right-2 flex items-center gap-1 rounded-full bg-black/70 px-2 py-0.5 text-[11px] font-semibold backdrop-blur"
						>
							<Star class="size-3 fill-amber-300 text-amber-300" />{entry.score}
						</span>
					{/if}
				</div>
				<p class="mt-2 line-clamp-2 text-[13px] leading-snug font-medium">{entry.item.title}</p>
				<p class="mt-0.5 flex items-center gap-1.5 text-[11px] text-faint">
					<span class="size-1.5 rounded-full" style="background: {STATUS_COLOR[entry.status]}"></span>
					{#if entry.status === 'In progress' && type !== 'movie'}
						{progressLabel(type, entry.progress, null)}
					{:else}
						{entry.status}
					{/if}
				</p>
			</a>
		{/each}
	</div>
{/if}
