<script lang="ts">
	import { CalendarRange, ChevronRight, Clock, NotebookPen, Repeat, Trash2 } from '@lucide/svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { ApiError, get, mutate, qs } from '#lib/api.ts';
	import BookExtras from '#lib/components/reading/BookExtras.svelte';
	import ActionBar from '#lib/components/library/ActionBar.svelte';
	import CastStrip from '#lib/components/library/CastStrip.svelte';
	import CountEpisodes from '#lib/components/library/CountEpisodes.svelte';
	import EntrySheets, { type SheetName } from '#lib/components/library/EntrySheets.svelte';
	import HistoryList from '#lib/components/library/HistoryList.svelte';
	import { ShowState } from '#lib/components/library/show.svelte.ts';
	import ShowEpisodes from '#lib/components/library/ShowEpisodes.svelte';
	import TitleHero from '#lib/components/library/TitleHero.svelte';
	import {
		formatDate,
		progressLabel,
		hasEpisodes,
		isCountAnime,
		isFilmKind,
		isMediaType,
		MEDIA,
		minutesLabel,
		titleHref,
		todayISO
	} from '#lib/media.ts';
	import { onSynced } from '#lib/offline.svelte.ts';
	import { settings } from '#lib/settings.svelte.ts';
	import { toast } from '#lib/toast.svelte.ts';
	import type { Entry, EntryDetail, MediaType, Status, TitleMetadata } from '#lib/types.ts';
	import { Button, Card, EmptyState, ListRow, PullToRefresh, Skeleton } from '#lib/ui/index.ts';

	const rawKind = $derived(page.params.type as string);
	// "show" (from season/episode events) is resolved to tv or anime below.
	const kind = $derived((['season', 'episode', 'show'].includes(rawKind) ? 'tv' : rawKind) as MediaType);
	const source = $derived(page.params.source as string);
	const mediaId = $derived(page.params.id as string);
	const hint = $derived(Number(page.url.searchParams.get('e')) || null);
	const meta = $derived(isMediaType(kind) ? MEDIA[kind] : MEDIA.tv);

	let entry = $state<EntryDetail | null>(null);
	let md = $state<TitleMetadata | null>(null);
	let mdLoading = $state(true);
	let phase = $state<'loading' | 'ready' | 'missing' | 'error'>('loading');
	let show = $state<ShowState | null>(null);
	let sheet = $state<SheetName>(null);
	let synopsisOpen = $state(false);
	let tracking = $state(false);
	let loadedFor = '';

	const base = $derived(`/library/${kind}/${entry?.id}`);

	async function findEntryId(): Promise<number | null> {
		if (hint) return hint;
		const found = await get<{ id: number | null }>(
			'/library/find' + qs({ media_type: kind, source, media_id: mediaId })
		);
		return found.id;
	}

	async function fetchEntry(id: number): Promise<EntryDetail | null> {
		try {
			return await get<EntryDetail>(`/library/${kind}/${id}?include_metadata=false`);
		} catch (e) {
			if (e instanceof ApiError && e.status === 404) return null;
			throw e;
		}
	}

	async function load() {
		const key = `${kind}/${source}/${mediaId}`;
		loadedFor = key;
		phase = 'loading';
		entry = null;
		md = null;
		show = null;
		mdLoading = true;
		sheet = null;

		get<TitleMetadata>(`/metadata/${kind}/${source}/${encodeURIComponent(mediaId)}`)
			.then((d) => {
				if (key !== loadedFor) return;
				md = d;
				// An old TMDB link to a show: the server answers with its TVDB series; follow it.
				const canonical = d.source;
				if ((kind === 'tv' || kind === 'anime') && canonical === 'tvdb' && source !== 'tvdb' && d.media_id) {
					void goto(titleHref({ source: 'tvdb', media_id: String(d.media_id) }, kind, hint), { replaceState: true });
				}
			})
			.catch(() => {})
			.finally(() => key === loadedFor && (mdLoading = false));

		try {
			let id = await findEntryId();
			let found = id ? await fetchEntry(id) : null;
			if (!found && hint) {
				// The hint was stale (entry deleted, or another user's link): look it up properly.
				id = (await get<{ id: number | null }>('/library/find' + qs({ media_type: kind, source, media_id: mediaId }))).id;
				found = id ? await fetchEntry(id) : null;
			}
			if (key !== loadedFor) return;
			entry = found;
			if (hasEpisodes(kind, source)) {
				show = new ShowState(source, mediaId, { onchange: adopt });
				void show.load();
			}
			phase = 'ready';
		} catch {
			if (key === loadedFor) phase = 'error';
		}
	}

	/** After an action on a title that was not tracked yet (marking an episode tracks it), pick up the entry. */
	async function adopt() {
		if (!entry) {
			try {
				const id = await findEntryId();
				if (id) entry = await fetchEntry(id);
			} catch {
				/* the page keeps working without it */
			}
		} else await refreshEntry();
	}

	/** Re-read the entry without disturbing what is on screen. */
	async function refreshEntry() {
		if (!entry) return;
		try {
			const next = await fetchEntry(entry.id);
			if (next) entry = next;
		} catch {
			/* keep what we have */
		}
	}

	$effect(() => {
		void `${rawKind}/${source}/${mediaId}`;
		if (['season', 'episode', 'show'].includes(rawKind)) {
			void resolveShow();
			return;
		}
		if (!isMediaType(kind)) {
			phase = 'missing';
			return;
		}
		load();
		return onSynced(() => {
			void refreshEntry();
			void show?.refresh();
		});
	});

	/** A show event doesn't say tv or anime: open whichever the user tracks (tv when neither). */
	async function resolveShow() {
		phase = 'loading';
		let target: { kind: MediaType; id: number | null } = { kind: 'tv', id: null };
		try {
			for (const k of ['tv', 'anime'] as const) {
				const found = await get<{ id: number | null }>('/library/find' + qs({ media_type: k, source, media_id: mediaId }));
				if (found.id) {
					target = { kind: k, id: found.id };
					break;
				}
			}
		} catch {
			/* fall through to tv */
		}
		await goto(titleHref({ source, media_id: mediaId }, target.kind, target.id), { replaceState: true });
	}

	async function reload() {
		await Promise.all([refreshEntry(), show?.refresh()]);
	}

	// --- Derived display -------------------------------------------------------------------------
	const title = $derived(entry?.item.title ?? md?.title ?? '');
	const image = $derived(md?.image || entry?.item.image || '');
	const genres = $derived((md?.genres ?? []).map((g) => (typeof g === 'string' ? g : g.name)));
	const details = $derived(
		Object.entries(md?.details ?? {}).filter(
			([, v]) => v !== null && v !== '' && (typeof v === 'string' || typeof v === 'number')
		) as [string, string | number][]
	);
	const year = $derived.by(() => {
		for (const [k, v] of Object.entries(md?.details ?? {})) {
			if (/release|first_air|start|publish|aired|date/i.test(k) && typeof v === 'string') {
				const y = v.match(/\b(1[89]\d\d|20\d\d)\b/)?.[1];
				if (y) return y;
			}
		}
		return null;
	});
	const total = $derived<number | null>((md?.max_progress as number | null | undefined) ?? null);
	const film = $derived(isFilmKind(kind));
	const mode = $derived<'episodes' | 'steps' | 'film' | 'none'>(
		film ? 'film' : show ? 'episodes' : meta.steppable ? 'steps' : 'none'
	);

	const nextAction = $derived.by(() => {
		if (!show || show.error) return null;
		const n = show.nextUp;
		if (n) return { label: `S${n.season}E${n.episode.episode_number}`, disabled: false };
		const up = show.upcoming;
		if (up) return { label: `Airs ${formatDate(up.air_date)}`, disabled: true, upcoming: true };
		return { label: show.show ? 'Caught up' : '…', disabled: true };
	});

	const stepDisplay = $derived(
		!entry
			? ''
			: kind === 'game'
				? minutesLabel(entry.progress)
				: kind === 'manga'
					? progressLabel(kind, entry.progress, total)
					: total
					? `${entry.progress}/${total}`
					: String(entry.progress)
	);

	const trackLabels = $derived<{ primary: [Status, string]; secondary: [Status, string] }>(
		film
			? { primary: ['Completed', 'Watched'], secondary: ['Planning', 'Watchlist'] }
			: kind === 'tv' || kind === 'anime'
				? { primary: ['In progress', 'Start'], secondary: ['Planning', 'Plan'] }
				: kind === 'game'
					? { primary: ['In progress', 'Playing'], secondary: ['Planning', 'Plan'] }
					: { primary: ['In progress', 'Start'], secondary: ['Planning', 'Plan'] }
	);

	// --- Writes (all through mutate: optimistic, offline-safe where the queue allows) ------------------
	function track(status: Status) {
		if (tracking) return;
		tracking = true;
		mutate<Entry>({
			path: '/library',
			body: { media_type: kind, source, media_id: mediaId, status },
			label: `${title || 'Title'} tracked`,
			onSuccess: async (created) => {
				toast(`Added to ${status === 'Planning' ? 'your plan' : 'your library'}`, { kind: 'success' });
				entry = await fetchEntry(created.id);
				if (entry) await goto(titleHref({ source, media_id: mediaId }, kind, entry.id), { replaceState: true });
				if (show) void show.refresh();
			}
		}).finally(() => (tracking = false));
	}

	function save(change: Record<string, unknown>, label: string, optimistic?: () => void | (() => void)) {
		const current = entry;
		if (!current) return;
		mutate({
			path: '/library',
			body: { media_type: kind, source, media_id: mediaId, instance_id: current.id, ...change },
			label,
			optimistic,
			onSuccess: () => {
				void refreshEntry();
				void show?.refresh();
			}
		});
	}

	function setStatus(next: Status) {
		const current = entry;
		if (!current) return;
		const before = { status: current.status, end: current.end_date };
		save({ status: next }, `${title}: ${next}`, () => {
			current.status = next;
			if (next === 'Completed' && !current.end_date) current.end_date = todayISO();
			return () => Object.assign(current, { status: before.status, end_date: before.end });
		});
	}

	function setScore(value: number | null) {
		const current = entry;
		if (!current) return;
		const before = current.score;
		save({ score: value ?? null }, value ? `${title}: score ${value}` : `${title}: score cleared`, () => {
			current.score = value;
			return () => (current.score = before);
		});
	}

	function saveDetails(v: { start_date: string; end_date: string; notes: string }) {
		const current = entry;
		if (!current) return;
		const before = { start: current.start_date, end: current.end_date, notes: current.notes };
		save(v, `${title}: dates and notes`, () => {
			current.start_date = v.start_date || null;
			current.end_date = v.end_date || null;
			current.notes = v.notes;
			return () => Object.assign(current, { start_date: before.start, end_date: before.end, notes: before.notes });
		});
	}

	function markWatched() {
		setStatus('Completed');
	}

	// A rewatch of a completed film starts a new run; show that run.
	function watchAgain() {
		const current = entry;
		if (!current) return;
		mutate<Entry>({
			path: `${base}/watches`,
			body: {},
			label: `${title}: watched again`,
			onSuccess: async (next) => {
				if (!next) return;
				const detail = await fetchEntry(next.id).catch(() => null);
				if (detail) entry = detail;
			}
		});
	}

	function step(direction: 1 | -1) {
		const current = entry;
		if (!current) return;
		const before = current.progress;
		mutate({
			path: `${base}/step`,
			body: { direction: direction === 1 ? 'up' : 'down' },
			label: `${title} ${direction === 1 ? '+1' : '-1'}`,
			optimistic: () => {
				current.progress = Math.max(0, before + direction);
				return () => (current.progress = before);
			},
			onSuccess: () => void refreshEntry()
		});
	}

	function remove() {
		const current = entry;
		if (!current) return;
		mutate({
			method: 'DELETE',
			path: `${base}`,
			label: `${title} removed`,
			onSuccess: () => {
				toast(`${title} removed from your library`, { kind: 'success' });
				goto(`/library/${kind}`, { replaceState: true });
			}
		});
	}

	// Started, then Finished — or, while not finished, when it was last
	// watched/read/played.
	const dateCells = $derived.by((): [string, string | null][] => {
		if (!entry) return [];
		const fmt = (d: string | null) => (d ? formatDate(d, { year: 'numeric' }) : null);
		const verb = ['book', 'manga', 'comic'].includes(kind) ? 'read' : ['game', 'boardgame'].includes(kind) ? 'played' : 'watched';
		const second: [string, string | null] = entry.end_date
			? ['Finished', fmt(entry.end_date)]
			: [`Last ${verb}`, fmt(entry.progressed_at)];
		return [['Started', fmt(entry.start_date)], second];
	});
	const bookItem = $derived<Record<string, unknown>>({
		...(entry?.item ?? { source, media_id: mediaId, media_type: 'book', title, image }),
		...(md ? { metadata: md } : {}),
		...(entry ? { status: entry.status } : {})
	});

	function back() {
		if (history.length > 1) history.back();
		else goto(`/library/${kind}`);
	}
</script>

<svelte:head><title>{title || 'Title'} · Encompass</title></svelte:head>

{#if phase === 'missing' || phase === 'error' || (phase === 'ready' && !entry && !md && !mdLoading)}
	<EmptyState
		title={phase === 'error' ? 'Could not load this title' : 'Title not found'}
		description={phase === 'error'
			? 'Check your connection and try again.'
			: 'It may have been removed, or the provider could not find it.'}
	>
		{#snippet actions()}
			{#if phase === 'error'}<Button variant="primary" onclick={load}>Retry</Button>{/if}
			<Button onclick={back}>Go back</Button>
		{/snippet}
	</EmptyState>
{:else if phase === 'loading'}
	<div aria-busy="true">
		<div class="grid grid-cols-[7.5rem_1fr] gap-4 pt-12 sm:grid-cols-[9rem_1fr]">
			<Skeleton class="aspect-[2/3] w-full" />
			<div class="space-y-3 self-end">
				<Skeleton class="h-4 w-20" />
				<Skeleton class="h-8 w-full" />
				<Skeleton class="h-4 w-1/2" />
			</div>
		</div>
		<div class="mt-6 space-y-3">
			<Skeleton shape="text" lines={4} />
			<Skeleton class="h-40 w-full" />
		</div>
	</div>
{:else}
	<PullToRefresh onrefresh={reload}>
		<TitleHero
			backdrop={md?.backdrop}
			{kind}
			{title}
			nativeTitle={md?.native_title}
			release={md?.release_status}
			{image}
			{year}
			{genres}
			rating={md?.score}
			{source}
			loadingMeta={mdLoading}
			{back}
		>
			<ActionBar
				tracked={!!entry}
				status={entry?.status ?? 'Planning'}
				score={entry?.score ?? null}
				{mode}
				next={nextAction}
				step={entry
					? {
							display: stepDisplay,
							short: kind === 'game' ? `${Math.round(entry.progress / 6) / 10}h` : String(entry.progress),
							label: meta.unit,
							canDecrement: entry.progress > 0,
							canIncrement: total === null || entry.progress < total
						}
					: undefined}
				film={{ done: entry?.status === 'Completed' }}
				track={trackLabels}
				onstatus={() => (sheet = 'status')}
				onscore={() => (sheet = 'score')}
				onnext={() => show?.markNext()}
				onstep={step}
				onwatched={markWatched}
				ontrack={track}
			/>
		</TitleHero>

		<div class="mt-5 grid gap-8 lg:grid-cols-[minmax(0,1fr)_22rem]">
			<div class="min-w-0 space-y-8">
				{#if md?.synopsis}
					<section>
						<p class="text-[0.9375rem] leading-relaxed text-ink/85 {synopsisOpen ? '' : 'line-clamp-4'}">{md.synopsis}</p>
						{#if md.synopsis.length > 220}
							<button
								type="button"
								class="mt-1 min-h-11 text-[0.8125rem] font-medium text-muted hover:text-ink"
								onclick={() => (synopsisOpen = !synopsisOpen)}
							>
								{synopsisOpen ? 'Show less' : 'Read more'}
							</button>
						{/if}
					</section>
				{:else if mdLoading}
					<Skeleton shape="text" lines={4} />
				{/if}

				{#if show}
					<section>
						<h2 class="mb-3 text-[0.9375rem] font-semibold">Episodes</h2>
						<ShowEpisodes state={show} accent={meta.accent} />
					</section>
				{:else if isCountAnime(kind, source) && ['mal', 'anilist'].includes(source)}
					<section>
						<h2 class="mb-3 text-[0.9375rem] font-semibold">Episodes</h2>
						<CountEpisodes {source} {mediaId} accent={meta.accent} onchange={adopt} />
					</section>
				{/if}

				{#if film && entry}
					<Card title="Watched" icon={Repeat} accent={meta.accent}>
						{#if entry.status === 'Completed'}
							<p class="text-[0.9375rem]">
								{entry.end_date ? `Watched on ${formatDate(entry.end_date, { year: 'numeric' })}` : 'Marked as watched'}
							</p>
						{:else}
							<p class="text-[0.9375rem] text-muted">Not watched yet. Use the Watched button below to log it for today.</p>
						{/if}
						<div class="mt-3 flex flex-wrap gap-2">
							{#if entry.status === 'Completed'}
								<Button size="sm" icon={Repeat} onclick={watchAgain}>Watched again</Button>
							{/if}
							<Button size="sm" icon={CalendarRange} onclick={() => (sheet = 'dates')}>Edit dates</Button>
						</div>
					</Card>
				{/if}

				{#if kind === 'game' && entry}
					<Card title="Playtime" icon={Clock} accent={meta.accent}>
						<p class="text-3xl font-semibold tabular-nums">{minutesLabel(entry.progress)}</p>
						<p class="mt-1 text-[0.8125rem] text-muted">The + button in the bar adds 30 minutes.</p>
					</Card>
				{/if}

				{#if ['tv', 'movie', 'anime', 'anime_movie'].includes(kind)}
					<CastStrip {kind} {source} {mediaId} accent={meta.accent} />
				{/if}

				{#if kind === 'book'}
					<BookExtras item={bookItem} entryId={entry?.id ?? null} />
				{/if}

				{#if entry}<HistoryList {entry} {kind} />{/if}
			</div>

			<aside class="min-w-0 space-y-4 lg:sticky lg:top-6 lg:self-start">
				{#if entry}
					<Card flush>
						<ul class="divide-y divide-line">
							<li>
								<button
									type="button"
									class="flex min-h-14 w-full items-center gap-3 px-4 py-3 text-left transition hover:bg-ink/5 active:bg-ink/8"
									onclick={() => (sheet = 'dates')}
								>
									<CalendarRange class="size-5 shrink-0 text-muted" />
									<span class="grid min-w-0 flex-1 grid-cols-2 gap-3">
										{#each dateCells as [label, value] (label)}
											<span class="min-w-0">
												<span class="block text-[0.75rem] tracking-wide text-faint uppercase">{label}</span>
												<span class="block text-[0.9375rem] font-medium {value ? '' : 'text-muted'}">{value ?? 'Not set'}</span>
											</span>
										{/each}
									</span>
									<ChevronRight class="size-4 shrink-0 text-faint" />
								</button>
							</li>
							<li>
								<ListRow title="Notes" subtitle={entry.notes || 'Add a note'} chevron onclick={() => (sheet = 'dates')}>
									{#snippet leading()}<NotebookPen class="size-5 shrink-0 text-muted" />{/snippet}
								</ListRow>
							</li>
							<li>
								<ListRow title="Remove from library" onclick={() => (sheet = 'delete')} class="text-danger">
									{#snippet leading()}<Trash2 class="size-5 shrink-0" />{/snippet}
								</ListRow>
							</li>
						</ul>
					</Card>
				{/if}

				{#if details.length}
					<Card title="Details">
						<dl class="grid grid-cols-2 gap-x-4 gap-y-3 lg:grid-cols-1">
							{#each details.slice(0, 10) as [key, value] (key)}
								<div class="min-w-0">
									<dt class="text-[0.75rem] tracking-wide text-faint uppercase">{key.replaceAll('_', ' ')}</dt>
									<dd class="mt-0.5 text-sm break-words">{value}</dd>
								</div>
							{/each}
						</dl>
					</Card>
				{/if}
			</aside>
		</div>

		<!-- Room for the pinned action bar on phones. -->
		<div class="h-32 lg:hidden" aria-hidden="true"></div>
	</PullToRefresh>

	{#if entry}
		<EntrySheets
			bind:which={sheet}
			status={entry.status}
			score={entry.score}
			start={entry.start_date}
			end={entry.end_date}
			notes={entry.notes}
			{kind}
			{title}
			onstatus={setStatus}
			onscore={setScore}
			onsave={saveDetails}
			ondelete={remove}
		/>
	{/if}
{/if}
