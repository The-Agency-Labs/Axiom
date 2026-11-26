<script lang="ts">
	import { Check, ChevronDown, Clock, ListChecks, RotateCcw, Star } from '@lucide/svelte';
	import { formatDate } from '#lib/media.ts';
	import type { ShowEpisode } from '#lib/types.ts';
	import { Button } from '#lib/ui/index.ts';

	/**
	 * One episode row: still, number + title, air date/runtime, expandable synopsis with
	 * "mark up to here" and "log a rewatch", and a 44px watch toggle. Unreleased episodes are greyed.
	 */
	let {
		ep,
		accent,
		next = false,
		missing = 0,
		ontoggle,
		onupto,
		onrewatch
	}: {
		ep: ShowEpisode;
		accent: string;
		/** The episode "mark next" will watch: highlighted. */
		next?: boolean;
		/** Aired, unwatched episodes before this one (shows "mark up to here"). */
		missing?: number;
		ontoggle: () => void;
		onupto?: () => void;
		onrewatch?: () => void;
	} = $props();

	let open = $state(false);
	const KIND_LABEL: Record<string, string> = { finale: 'Finale', mid_season: 'Mid-season finale' };
	// Unreleased episodes can only be unmarked (if somehow watched), not marked.
	const locked = $derived(!ep.aired && !ep.watched);
</script>

<li
	class="px-3 py-2 {next ? 'bg-brand/8 shadow-[inset_3px_0_0_var(--color-brand)]' : ''} {ep.aired || ep.watched ? '' : 'opacity-55'}"
>
	<div class="flex items-center gap-3">
		<button
			type="button"
			onclick={() => (open = !open)}
			aria-expanded={open}
			class="flex min-h-14 min-w-0 flex-1 items-center gap-3 rounded-lg text-left"
		>
			<span class="relative grid h-12 w-[4.5rem] shrink-0 place-items-center overflow-hidden rounded-lg bg-raised ring-1 ring-line">
				{#if ep.image}
					<img src={ep.image} alt="" loading="lazy" class="h-full w-full object-cover" />
				{:else}
					<span class="text-base font-semibold text-faint tabular-nums">{ep.episode_number}</span>
				{/if}
				{#if ep.watched}<span class="absolute inset-0 grid place-items-center bg-black/45"><Check class="size-5 text-white" /></span>{/if}
			</span>
			<span class="min-w-0 flex-1">
				<span class="flex items-baseline gap-2 text-[0.9375rem] font-medium">
					<span class="shrink-0 text-faint tabular-nums">{ep.episode_number}</span>
					<span class="truncate">{ep.title}</span>
				</span>
				<span class="mt-0.5 flex flex-wrap items-center gap-x-2 text-xs text-faint">
					{#if ep.air_date}<span>{ep.aired ? '' : 'Airs '}{formatDate(ep.air_date, { year: 'numeric' })}</span>{/if}
					{#if ep.runtime}<span class="flex items-center gap-1"><Clock class="size-3" />{ep.runtime}m</span>{/if}
					{#if ep.score}<span class="flex items-center gap-1"><Star class="size-3" />{ep.score.toFixed(1)}</span>{/if}
					{#if KIND_LABEL[ep.kind]}<span class="rounded-full border border-line px-1.5 text-[0.75rem] text-muted">{KIND_LABEL[ep.kind]}</span>{/if}
					{#if next}<span class="font-semibold text-brand">Up next</span>{/if}
				</span>
			</span>
			<ChevronDown class="size-4 shrink-0 text-faint transition {open ? 'rotate-180' : ''}" />
		</button>
		<button
			type="button"
			onclick={ontoggle}
			disabled={locked}
			class="grid size-11 shrink-0 place-items-center rounded-full border transition active:scale-90 disabled:opacity-40 {ep.watched
				? 'border-transparent text-black'
				: 'border-line-strong text-muted hover:text-ink'}"
			style={ep.watched ? `background: ${accent}` : ''}
			aria-label={ep.watched ? `Unmark episode ${ep.episode_number}` : `Mark episode ${ep.episode_number} watched`}
			aria-pressed={ep.watched}
		>
			<Check class="size-5" />
		</button>
	</div>
	{#if open}
		<div class="pt-1 pb-2 pl-1">
			{#if ep.overview}<p class="text-[0.8125rem] leading-relaxed text-muted">{ep.overview}</p>{/if}
			{#if ep.watched && ep.watched_at}
				<p class="mt-1 text-xs text-faint">
					Watched {formatDate(ep.watched_at, { year: 'numeric' })}{ep.watch_count > 1 ? ` · ${ep.watch_count} times` : ''}
				</p>
			{/if}
			{#if (missing > 0 && !ep.watched && onupto) || (ep.watched && onrewatch)}
				<div class="mt-2 flex flex-wrap gap-2">
					{#if missing > 0 && !ep.watched && onupto}
						<Button size="sm" icon={ListChecks} onclick={onupto}>Mark up to here ({missing + 1})</Button>
					{/if}
					{#if ep.watched && onrewatch}
						<Button size="sm" icon={RotateCcw} onclick={onrewatch}>Log a rewatch</Button>
					{/if}
				</div>
			{/if}
		</div>
	{/if}
</li>
