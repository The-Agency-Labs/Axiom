<script lang="ts">
	import { Check, Clock, Link2 } from '@lucide/svelte';
	import { mutate } from '#lib/api.ts';
	import Cover from '#lib/components/Cover.svelte';
	import { bookPosition, loadChapters } from '#lib/chapters.svelte.ts';
	import { formatDate, hasEpisodes, MEDIA, minutesLabel, progressLabel, relativeTime, titleHref, todayISO } from '#lib/media.ts';
	import { toast } from '#lib/toast.svelte.ts';
	import type { MediaType, NowEntry } from '#lib/types.ts';
	import { Button, ProgressBar, Stepper } from '#lib/ui/index.ts';

	/**
	 * One in-progress title with its one-tap action: "mark next episode" for shows and TMDB anime,
	 * a +1 stepper for count-based kinds. Updates optimistically, with an Undo toast.
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
		if (episodic) return `Season ${season}${entry.next ? '' : ' · caught up'}`;
		if (entry.last_session)
			return `Last read ${relativeTime(entry.last_session.started_at)} · ${entry.last_session.pages_read} pages`;
		if (entry.progressed_at) return `Updated ${relativeTime(entry.progressed_at)}`;
		return entry.genres?.slice(0, 2).join(' · ') ?? '';
	});

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

	function step(direction: 1 | -1) {
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
	<a {href} class="flex gap-3 rounded-xl" aria-label={entry.title}>
		<Cover src={entry.image} title={entry.title} type={kind} class="aspect-[2/3] w-16 shrink-0 sm:w-20" />
		<span class="flex min-w-0 flex-1 flex-col py-0.5">
			<span class="line-clamp-2 text-[0.9375rem] leading-snug font-semibold">{entry.title}</span>
			<span class="mt-0.5 line-clamp-1 text-[0.8125rem] text-muted">{subtitle}</span>
			<span class="mt-auto flex items-baseline justify-between pt-2 text-xs">
				<span class="min-w-0 truncate font-medium tabular-nums">{progressText}</span>
				{#if percent !== null}<span class="text-faint tabular-nums">{Math.round(percent)}%</span>{/if}
			</span>
			<ProgressBar {percent} accent={meta.accent} label="{entry.title} progress" class="mt-1" />
		</span>
	</a>

	{#if episodic && entry.next}
		<Button
			variant={upcoming || tracker ? 'secondary' : 'primary'}
			full
			disabled={upcoming}
			class="mt-3"
			icon={upcoming ? Clock : tracker ? Link2 : Check}
			onclick={markNext}
			aria-label={upcoming
				? `Episode ${entry.next.number} airs ${formatDate(entry.next.air_date)}`
				: tracker
					? `Episode ${entry.next.number}, tracked by ${tracker}. Mark it watched by hand`
					: `Mark episode ${entry.next.number} watched`}
		>
			{#if upcoming}
				E{entry.next.number} airs {formatDate(entry.next.air_date)}
			{:else if tracker}
				<span class="truncate">E{entry.next.number} · from {tracker}</span>
			{:else}
				<span class="truncate">Watched E{entry.next.number}{entry.next.title ? ` · ${entry.next.title}` : ''}</span>
			{/if}
		</Button>
	{:else if canStep}
		<div class="mt-3 flex items-center justify-between gap-3">
			<span class="flex min-w-0 items-center gap-1.5 text-[0.8125rem] text-muted">
				{#if tracker}<Link2 class="size-3.5 shrink-0" /><span class="truncate">From {tracker}</span>
				{:else}{meta.unit === '30 min' ? 'Add 30 min' : `Add ${meta.unit}`}{/if}
			</span>
			<Stepper
				display={kind === 'game' ? minutesLabel(entry.progress) : String(entry.progress)}
				label={meta.unit}
				onstep={step}
				canDecrement={entry.progress > 0}
				canIncrement={!atEnd}
			/>
		</div>
	{/if}
</article>
