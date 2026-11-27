<script lang="ts">
	import { mutate } from '#lib/api.ts';
	import AppLogo from '#lib/components/AppLogo.svelte';
	import Cover from '#lib/components/Cover.svelte';
	import { bookPosition, loadChapters } from '#lib/chapters.svelte.ts';
	import { formatDate, hasEpisodes, MEDIA, minutesLabel, progressLabel, relativeTime, titleHref, todayISO } from '#lib/media.ts';
	import { toast } from '#lib/toast.svelte.ts';
	import type { MediaType, NowEntry } from '#lib/types.ts';
	import { ProgressBar, Stepper } from '#lib/ui/index.ts';

	/**
	 * One in-progress title with its one-tap -/+ stepper: shows step through their season's episodes,
	 * count-based kinds their chapters, pages or time. Updates optimistically, with an Undo toast.
	 */
	let { entry, kind, onchange }: { entry: NowEntry; kind: MediaType; onchange?: () => void } = $props();

	const meta = $derived(MEDIA[kind]);
	const href = $derived(titleHref(entry.item, kind, entry.id));
	const season = $derived(entry.season_number ?? entry.item.season_number);
	const episodic = $derived(hasEpisodes(kind, entry.item.source) && season != null);
	const upcoming = $derived(!!entry.next?.air_date && entry.next.air_date > todayISO());
	const percent = $derived(entry.total ? (entry.progress / entry.total) * 100 : null);
	const atEnd = $derived(entry.total !== null && entry.progress >= entry.total);
	const canStep = $derived(!episodic && meta.steppable);
	$effect(() => {
		if (kind === 'book') loadChapters();
	});
	const progressText = $derived(
		(kind === 'book' && bookPosition(entry.item.id, entry.progress)) || progressLabel(kind, entry.progress, entry.total)
	);

	/** The apps that track this title, when any do: its buttons then step back. */
	const tracker = $derived(entry.linked?.join(' and ') ?? '');

	const subtitle = $derived.by(() => {
		// Nothing aired is left to watch: caught up, whether or not the next one is dated.
		if (episodic) return !entry.next || upcoming ? `Season ${season} · caught up` : `Season ${season}`;
		if (entry.last_session)
			return `Last read ${relativeTime(entry.last_session.started_at)} · ${entry.last_session.pages_read} pages`;
		if (entry.progressed_at) return `Updated ${relativeTime(entry.progressed_at)}`;
		return entry.genres?.slice(0, 2).join(' · ') ?? '';
	});

	/** The next episode's date, for the calendar tile of a show that is waiting for it. */
	const due = $derived(
		episodic && upcoming && entry.next
			? {
					episode: entry.next.number,
					month: formatDate(entry.next.air_date, { month: 'short', day: undefined }),
					day: formatDate(entry.next.air_date, { month: undefined, day: 'numeric' })
				}
			: null
	);

	const epPath = $derived(`/shows/${entry.item.source}/${entry.item.media_id}/seasons/${season}/episodes`);

	function markNext() {
		const next = entry.next;
		if (!next || upcoming) return;
		const before = { progress: entry.progress, next: entry.next };
		mutate({
			path: epPath,
			body: { episodes: [next.number], watched: true },
			label: `${entry.title} E${next.number} watched`,
			// A tracked title changes only after the user says yes, so nothing moves before that.
			optimistic: tracker
				? undefined
				: () => {
						entry.progress += 1;
						entry.next = null;
						return () => Object.assign(entry, before);
					},
			onSuccess: () => {
				if (tracker) {
					entry.progress = before.progress + 1;
					entry.next = null;
				}
				toast(`${entry.title} · E${next.number} watched`, {
					kind: 'success',
					action: {
						label: 'Undo',
						run: () =>
							// A manual tap may unwatch.
							mutate({
								path: epPath,
								body: { episodes: [next.number], watched: false },
								label: `${entry.title} E${next.number} unmarked`,
								override: !!tracker,
								optimistic: () => {
									Object.assign(entry, before);
								},
								onSuccess: () => onchange?.()
							})
					}
				});
				onchange?.();
			}
		});
	}

	/** The minus on a show: unwatch the episode the card is at. */
	function unmarkLast() {
		const number = entry.progress;
		if (!number) return;
		mutate({
			path: epPath,
			body: { episodes: [number], watched: false },
			label: `${entry.title} E${number} unmarked`,
			optimistic: tracker
				? undefined
				: () => {
						entry.progress = number - 1;
						return () => (entry.progress = number);
					},
			onSuccess: () => onchange?.()
		});
	}

	function step(direction: 1 | -1) {
		if (episodic) return direction === 1 ? markNext() : unmarkLast();
		const before = entry.progress;
		mutate({
			path: `/library/${kind}/${entry.id}/step`,
			body: { direction: direction === 1 ? 'up' : 'down' },
			label: `${entry.title} ${direction === 1 ? '+1' : '-1'}`,
			optimistic: tracker
				? undefined
				: () => {
						entry.progress = Math.max(0, before + direction);
						return () => (entry.progress = before);
					},
			onSuccess: () => {
				if (tracker) entry.progress = Math.max(0, before + direction);
				if (direction === 1)
					toast(`${entry.title} · ${progressLabel(kind, entry.progress, entry.total)}`, {
						kind: 'success',
						action: {
							label: 'Undo',
							run: () =>
								mutate({
									path: `/library/${kind}/${entry.id}/step`,
									body: { direction: 'down' },
									label: `${entry.title} -1`,
									override: !!tracker,
									optimistic: () => {
										entry.progress = Math.max(0, entry.progress - 1);
									},
									onSuccess: () => onchange?.()
								})
						}
					});
				onchange?.();
			}
		});
	}
</script>

<article class="min-w-0 rounded-[var(--radius-card)] border border-line bg-surface p-3">
	<a {href} class="flex gap-3 rounded-xl" aria-label={due ? `${entry.title}, episode ${due.episode} airs ${due.month} ${due.day}` : entry.title}>
		<Cover src={entry.image} title={entry.title} type={kind} class="aspect-[2/3] w-16 shrink-0 sm:w-20" />
		<span class="flex min-w-0 flex-1 flex-col py-0.5">
			<span class="flex items-start gap-2">
				<span class="min-w-0 flex-1">
					<span class="line-clamp-2 text-[0.9375rem] leading-snug font-semibold">{entry.title}</span>
					<span class="mt-0.5 line-clamp-1 text-[0.8125rem] text-muted">{subtitle}</span>
				</span>
				{#if due}
					<span class="flex w-12 shrink-0 flex-col overflow-hidden rounded-xl border border-line-strong bg-raised text-center leading-tight" aria-hidden="true">
						<span class="py-0.5 text-[0.6875rem] font-semibold" style="background: color-mix(in oklab, {meta.accent} 22%, transparent); color: {meta.accent}">E{due.episode}</span>
						<span class="pt-0.5 text-[0.625rem] font-medium text-muted uppercase">{due.month}</span>
						<span class="pb-1 text-base font-semibold tabular-nums">{due.day}</span>
					</span>
				{/if}
			</span>
			<span class="mt-auto flex items-baseline justify-between pt-2 text-xs">
				<span class="min-w-0 truncate font-medium tabular-nums">{progressText}</span>
				{#if percent !== null}<span class="text-faint tabular-nums">{Math.round(percent)}%</span>{/if}
			</span>
			<ProgressBar {percent} accent={meta.accent} label="{entry.title} progress" class="mt-1" />
		</span>
	</a>

	{#if episodic || canStep}
		<div class="mt-3 flex items-center justify-between gap-3">
			<span class="flex min-w-0 items-center gap-1.5 text-[0.8125rem] text-muted">
				{#if tracker}{#each entry.linked ?? [] as app (app)}<AppLogo name={app} class="h-8" />{/each}
				{:else}{meta.unit === '30 min' ? 'Add 30 min' : `Add ${episodic ? 'episode' : meta.unit}`}{/if}
			</span>
			<Stepper
				display={kind === 'game' ? minutesLabel(entry.progress) : String(entry.progress)}
				label={episodic ? 'episode' : meta.unit}
				onstep={step}
				canDecrement={entry.progress > 0}
				canIncrement={episodic ? !!entry.next && !upcoming : !atEnd}
			/>
		</div>
	{/if}
</article>
