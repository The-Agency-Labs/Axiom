<script lang="ts">
	import { ArrowLeft, Check, LoaderCircle, Minus, Plus, Star, Trash2 } from '@lucide/svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { del, get, post } from '#lib/api.ts';
	import Cover from '#lib/components/Cover.svelte';
	import EpisodeBrowser from '#lib/components/EpisodeBrowser.svelte';
	import ProgressBar from '#lib/components/ProgressBar.svelte';
	import { formatDate, MEDIA, progressLabel, relativeTime, STATUSES } from '#lib/media.ts';
	import type { EntryDetail, MediaType, Status } from '#lib/types.ts';

	const type = $derived(page.params.type as MediaType);
	const id = $derived(page.params.id);
	const meta = $derived(MEDIA[type] ?? MEDIA.tv);

	let entry = $state<EntryDetail | null>(null);
	let error = $state('');
	let saving = $state(false);
	let stepping = $state(false);
	let saved = $state(false);

	let status = $state<Status>('In progress');
	let score = $state<string>('');
	let notes = $state('');

	async function load() {
		error = '';
		try {
			entry = await get<EntryDetail>(`/library/${type}/${id}`);
			status = entry.status;
			score = entry.score?.toString() ?? '';
			notes = entry.notes;
		} catch {
			error = 'Not found.';
		}
	}

	/** Re-read the entry after episodes change, keeping unsaved edits. */
	async function refresh() {
		try {
			const next = await get<EntryDetail>(`/library/${type}/${id}?include_metadata=false`);
			entry = { ...next, metadata: entry?.metadata ?? null };
		} catch {
			/* keep what we have */
		}
	}

	$effect(() => {
		void type;
		void id;
		load();
	});

	const md = $derived(entry?.metadata ?? {});
	const title = $derived(
		type === 'season' && md.season_title ? `${entry?.item.title} · ${md.season_title}` : (entry?.item.title ?? '')
	);
	const total = $derived<number | null>(md.max_progress ?? null);
	const percent = $derived(entry && total ? (entry.progress / total) * 100 : null);
	const genres = $derived<string[]>((md.genres ?? []).map((g: any) => (typeof g === 'string' ? g : g.name)));
	const details = $derived(
		Object.entries(md.details ?? {}).filter(
			([, v]) => v !== null && v !== '' && (typeof v === 'string' || typeof v === 'number')
		) as [string, string | number][]
	);

	async function save() {
		if (!entry) return;
		saving = true;
		try {
			await post('/library', {
				media_type: type,
				source: entry.item.source,
				media_id: entry.item.media_id,
				season_number: entry.item.season_number,
				instance_id: entry.id,
				status,
				score: score === '' ? null : Number(score),
				notes
			});
			await load();
			saved = true;
			setTimeout(() => (saved = false), 1600);
		} finally {
			saving = false;
		}
	}

	async function step(direction: 'up' | 'down') {
		if (!entry) return;
		stepping = true;
		try {
			await post(`/library/${type}/${entry.id}/step`, { direction });
			await load();
		} finally {
			stepping = false;
		}
	}

	async function remove() {
		if (!entry || !confirm(`Remove ${entry.item.title} from your library?`)) return;
		await del(`/library/${type}/${entry.id}`);
		goto(`/library/${type === 'season' ? 'tv' : type}`);
	}
</script>

<svelte:head><title>{title || 'Loading'} · Encompass</title></svelte:head>

<button
	onclick={() => history.back()}
	class="mb-6 flex items-center gap-1.5 text-sm text-muted transition hover:text-ink"
>
	<ArrowLeft class="size-4" /> Back
</button>

{#if error}
	<p class="text-muted">{error}</p>
{:else if !entry}
	<div class="flex gap-8">
		<div class="aspect-[2/3] w-48 animate-pulse rounded-2xl bg-white/5"></div>
		<div class="flex-1 space-y-3">
			<div class="h-8 w-2/3 animate-pulse rounded bg-white/5"></div>
			<div class="h-4 w-1/3 animate-pulse rounded bg-white/5"></div>
		</div>
	</div>
{:else}
	<div class="relative">
		<!-- Soft backdrop from the cover art -->
		<div class="pointer-events-none absolute -inset-x-10 -top-24 h-80 overflow-hidden opacity-30">
			<img src={md.image || entry.item.image} alt="" class="h-full w-full scale-110 object-cover blur-3xl" />
			<div class="absolute inset-0 bg-gradient-to-b from-transparent to-[var(--color-canvas)]"></div>
		</div>

		<div class="relative grid gap-8 md:grid-cols-[13rem_1fr] lg:grid-cols-[15rem_1fr]">
			<Cover
				src={md.image || entry.item.image}
				{title}
				type={entry.media_type}
				class="mx-auto aspect-[2/3] w-44 shadow-2xl shadow-black/50 md:mx-0 md:w-full"
			/>

			<div class="min-w-0">
				<div class="flex items-center gap-2 text-xs font-medium" style="color: {meta.accent}">
					<meta.icon class="size-3.5" />
					{meta.label}
					{#if md.score}
						<span class="ml-2 flex items-center gap-1 text-muted">
							<Star class="size-3 fill-amber-300 text-amber-300" />{md.score}
						</span>
					{/if}
				</div>
				<h1 class="mt-2 text-3xl font-semibold tracking-tight text-balance sm:text-4xl">{title}</h1>

				{#if genres.length}
					<div class="mt-3 flex flex-wrap gap-1.5">
						{#each genres.slice(0, 6) as genre (genre)}
							<span class="rounded-full border border-line px-2.5 py-0.5 text-xs text-muted">{genre}</span>
						{/each}
					</div>
				{/if}

				<!-- Progress -->
				{#if type !== 'movie' && type !== 'tv'}
					<div class="mt-6 rounded-2xl border border-line bg-surface p-4">
						<div class="flex items-center justify-between gap-4">
							<div>
								<p class="text-xs text-muted">Progress</p>
								<p class="mt-0.5 text-lg font-semibold tabular-nums">
									{progressLabel(type, entry.progress, total)}
								</p>
							</div>
							{#if meta.steppable}
								<div class="flex items-center gap-1.5">
									<button
										onclick={() => step('down')}
										disabled={stepping || entry.progress === 0}
										class="grid size-9 place-items-center rounded-full border border-line text-muted transition hover:text-ink disabled:opacity-40"
										aria-label="Minus one {meta.unit}"
									>
										<Minus class="size-4" />
									</button>
									<button
										onclick={() => step('up')}
										disabled={stepping || (total !== null && entry.progress >= total)}
										class="grid size-9 place-items-center rounded-full font-semibold transition disabled:opacity-40"
										style="background: {meta.accent}; color: #09090b"
										aria-label="Plus one {meta.unit}"
									>
										{#if stepping}<LoaderCircle class="size-4 animate-spin" />{:else}<Plus class="size-4" />{/if}
									</button>
								</div>
							{/if}
						</div>
						{#if percent !== null}
							<ProgressBar {percent} accent={meta.accent} class="mt-3" />
						{/if}
					</div>
				{/if}

				{#if md.synopsis}
					<p class="mt-6 max-w-3xl text-[15px] leading-relaxed text-ink/80">{md.synopsis}</p>
				{/if}

				{#if details.length}
					<dl class="mt-6 grid grid-cols-2 gap-x-6 gap-y-3 sm:grid-cols-3 lg:grid-cols-4">
						{#each details.slice(0, 8) as [key, value] (key)}
							<div>
								<dt class="text-[11px] tracking-wide text-faint uppercase">{key.replaceAll('_', ' ')}</dt>
								<dd class="mt-0.5 truncate text-sm">{value}</dd>
							</div>
						{/each}
					</dl>
				{/if}
			</div>
		</div>
	</div>

	<div class="mt-10 grid gap-6 lg:grid-cols-[minmax(0,1fr)_20rem]">
		<div class="min-w-0 space-y-6">
			<!-- Seasons, episodes and specials -->
			{#if (type === 'tv' || type === 'season') && entry.item.source !== 'manual'}
				<section>
					<h2 class="mb-3 text-[15px] font-semibold">Episodes</h2>
					<EpisodeBrowser
						source={entry.item.source}
						mediaId={entry.item.media_id}
						initialSeason={type === 'season' ? entry.item.season_number : null}
						accent={meta.accent}
						onchange={refresh}
					/>
				</section>
			{/if}

			<!-- History -->
			{#if entry.history.length}
				<section>
					<h2 class="mb-3 text-[15px] font-semibold">History</h2>
					<ol class="space-y-3 border-l border-line pl-5">
						{#each [...entry.history].reverse().slice(0, 12) as row, i (i)}
							<li class="relative text-sm">
								<span class="absolute top-1.5 -left-[1.4rem] size-2 rounded-full bg-line-strong"></span>
								<span class="text-muted capitalize">{row.type}</span>
								{#if row.status}<span> · {row.status}</span>{/if}
								{#if row.progress}<span class="text-muted"> · {row.progress} {meta.units}</span>{/if}
								<span class="block text-xs text-faint">{relativeTime(row.date)}</span>
							</li>
						{/each}
					</ol>
				</section>
			{/if}
		</div>

		<!-- Edit panel -->
		<aside class="h-fit space-y-4 rounded-2xl border border-line bg-surface p-4 lg:sticky lg:top-6">
			<label class="block space-y-1.5">
				<span class="text-xs font-medium text-muted">Status</span>
				<select
					bind:value={status}
					class="w-full rounded-lg border border-line bg-raised px-3 py-2 text-sm outline-none focus:border-line-strong"
				>
					{#each STATUSES as s (s)}<option value={s}>{s}</option>{/each}
				</select>
			</label>
			<label class="block space-y-1.5">
				<span class="text-xs font-medium text-muted">Your score (0–10)</span>
				<input
					type="number"
					min="0"
					max="10"
					step="0.5"
					bind:value={score}
					class="w-full rounded-lg border border-line bg-raised px-3 py-2 text-sm outline-none focus:border-line-strong"
				/>
			</label>
			<label class="block space-y-1.5">
				<span class="text-xs font-medium text-muted">Notes</span>
				<textarea
					rows="4"
					bind:value={notes}
					placeholder="Thoughts, favourite moments, where you left off…"
					class="w-full resize-y rounded-lg border border-line bg-raised px-3 py-2 text-sm outline-none focus:border-line-strong"
				></textarea>
			</label>
			<button
				onclick={save}
				disabled={saving}
				class="flex w-full items-center justify-center gap-2 rounded-lg bg-ink py-2 text-sm font-semibold text-canvas transition hover:bg-white disabled:opacity-60"
			>
				{#if saved}<Check class="size-4" /> Saved{:else}{saving ? 'Saving…' : 'Save'}{/if}
			</button>
			<div class="flex items-center justify-between pt-1 text-xs text-faint">
				<span>
					{#if entry.start_date}Started {formatDate(entry.start_date, { year: 'numeric' })}{/if}
				</span>
				<button onclick={remove} class="flex items-center gap-1 transition hover:text-rose-400">
					<Trash2 class="size-3.5" /> Remove
				</button>
			</div>
		</aside>
	</div>
{/if}
