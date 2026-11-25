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
	// The Anime section mixes series (anime and TMDB shows) and films.
	let form = $state<'All' | 'Series' | 'Films'>('All');
	let year = $state<number | null>(null);
	let years = $state<number[]>([]);

	$effect(() => {
		void type;
		year = null;
		form = 'All';
	});

	const isFilm = (e: Entry) => e.media_type === 'movie';
	const formCounts = $derived({
		All: entries?.length ?? 0,
		Series: (entries ?? []).filter((e) => !isFilm(e)).length,
		Films: (entries ?? []).filter(isFilm).length
	});

	$effect(() => {
		const current = type;
		const currentYear = year;
		entries = null;
		get<{ results: Entry[]; years: number[] }>(
			'/library' + qs({ media_type: current, year: currentYear, limit: 1000 })
		).then((data) => {
			if (current !== type || currentYear !== year) return;
			entries = data.results;
			years = data.years;
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
				(form === 'All' || (form === 'Films') === isFilm(e)) &&
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
			<p class="text-sm text-faint">{counts.All} {year ? `active in ${year}` : 'tracked'}</p>
		</div>
	</div>
	<div class="flex w-full items-center gap-2 sm:w-auto">
	{#if years.length}
		<select
			bind:value={year}
			aria-label="Filter by year"
			class="shrink-0 rounded-xl border border-line bg-surface px-3 py-2 text-sm outline-none transition focus:border-line-strong"
		>
			<option value={null}>All years</option>
			{#each years as y (y)}<option value={y}>{y}</option>{/each}
		</select>
	{/if}
	<label class="relative w-full sm:w-64">
		<Search class="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-faint" />
		<input
			bind:value={filter}
			placeholder="Filter {meta.plural.toLowerCase()}…"
			class="w-full rounded-xl border border-line bg-surface py-2 pr-3 pl-9 text-sm outline-none transition focus:border-line-strong"
		/>
	</label>
	</div>
</header>

{#if type === 'anime' && formCounts.Films}
	<div class="mb-3 inline-flex rounded-xl border border-line bg-surface p-1 text-sm" role="group" aria-label="Series or films">
		{#each ['All', 'Series', 'Films'] as option (option)}
			<button
				onclick={() => (form = option as typeof form)}
				aria-pressed={form === option}
				class="flex items-center gap-2 rounded-lg px-3 py-1 font-medium transition {form === option
					? 'bg-ink/8 text-ink'
					: 'text-muted hover:text-ink'}"
			>
				{option}
				<span class="text-xs text-faint tabular-nums">{formCounts[option as typeof form]}</span>
			</button>
		{/each}
	</div>
{/if}

<div class="shelf -mx-4 mb-6 flex gap-1.5 overflow-x-auto px-4 sm:mx-0 sm:px-0">
	{#each ['All', ...STATUSES] as status (status)}
		<button
			onclick={() => (tab = status as Status | 'All')}
			class="flex shrink-0 items-center gap-2 rounded-full border px-3.5 py-1.5 text-sm font-medium transition {tab === status
				? 'border-line-strong bg-ink/8 text-ink'
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
			<div class="aspect-[2/3] animate-pulse rounded-xl bg-ink/[0.04]"></div>
		{/each}
	</div>
{:else if visible.length === 0}
	<div class="rounded-2xl border border-dashed border-line-strong p-10 text-center text-sm text-muted">
		Nothing here yet. <a href="/search?type={type}" class="text-ink underline underline-offset-4">Find something to track</a>.
	</div>
{:else}
	<div class="grid grid-cols-2 gap-x-4 gap-y-6 sm:grid-cols-4 md:grid-cols-5 xl:grid-cols-7">
		{#each visible as entry (entry.id)}
			<a href="/item/{entry.media_type}/{entry.id}" class="group">
				<div class="relative">
					<Cover
						src={entry.item.image}
						title={entry.item.title}
						type={entry.media_type}
						class="aspect-[2/3] w-full transition duration-300 group-hover:-translate-y-0.5 group-hover:ring-line-strong"
					/>
					{#if entry.score}
						<span
							class="absolute top-2 right-2 flex items-center gap-1 rounded-full bg-black/70 px-2 py-0.5 text-[11px] font-semibold text-white backdrop-blur"
						>
							<Star class="size-3 fill-amber-300 text-amber-300" />{entry.score}
						</span>
					{/if}
				</div>
				<p class="mt-2 line-clamp-2 text-[13px] leading-snug font-medium">{entry.item.title}</p>
				<p class="mt-0.5 flex items-center gap-1.5 text-[11px] text-faint">
					<span class="size-1.5 rounded-full" style="background: {STATUS_COLOR[entry.status]}"></span>
					{#if entry.media_type === 'tv' && entry.max_progress}
						{Math.round((entry.progress / entry.max_progress) * 100)}% · {entry.progress}/{entry.max_progress} ep
					{:else if entry.status === 'In progress' && entry.media_type !== 'movie'}
						{progressLabel(entry.media_type, entry.progress, null)}
					{:else}
						{entry.status}
					{/if}
				</p>
			</a>
		{/each}
	</div>
{/if}
