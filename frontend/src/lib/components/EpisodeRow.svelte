<script lang="ts">
	import { Check, ChevronDown, Clock, LoaderCircle, Star } from '@lucide/svelte';
	import { formatDate } from '#lib/media.ts';
	import type { ShowEpisode } from '#lib/types.ts';

	/** One episode: still, title, meta, expandable synopsis and a watch toggle. */
	let {
		ep,
		accent,
		busy = false,
		ontoggle,
		onrewatch
	}: {
		ep: ShowEpisode;
		accent: string;
		busy?: boolean;
		ontoggle: () => void;
		onrewatch?: () => void;
	} = $props();

	let open = $state(false);
	const KIND_LABEL: Record<string, string> = { finale: 'Finale', mid_season: 'Mid-season finale' };
</script>

<li class="flex gap-3 p-3 sm:gap-4 {ep.aired ? '' : 'opacity-60'}">
	<button
		onclick={() => (open = !open)}
		class="relative aspect-video w-24 shrink-0 overflow-hidden rounded-lg bg-white/5 sm:w-36"
		aria-label="Show details for episode {ep.episode_number}"
	>
		{#if ep.image}
			<img src={ep.image} alt="" loading="lazy" class="h-full w-full object-cover" />
		{:else}
			<span class="grid h-full place-items-center text-lg font-semibold text-faint tabular-nums">{ep.episode_number}</span>
		{/if}
		{#if ep.watched}
			<span class="absolute inset-0 bg-black/35"></span>
		{/if}
	</button>
	<div class="min-w-0 flex-1">
		<button onclick={() => (open = !open)} class="block w-full text-left">
			<p class="flex items-baseline gap-2 text-sm font-medium">
				<span class="shrink-0 text-faint tabular-nums">{ep.episode_number}</span>
				<span class={open ? '' : 'truncate'}>{ep.title}</span>
			</p>
			<p class="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-xs text-faint">
				{#if ep.air_date}
					<span>{ep.aired ? '' : 'Airs '}{formatDate(ep.air_date, { year: 'numeric' })}</span>
				{/if}
				{#if ep.runtime}<span class="flex items-center gap-1"><Clock class="size-3" />{ep.runtime}m</span>{/if}
				{#if ep.score}<span class="flex items-center gap-1"><Star class="size-3" />{ep.score.toFixed(1)}</span>{/if}
				{#if KIND_LABEL[ep.kind]}
					<span class="rounded-full border border-line px-1.5 text-[10px] text-muted">{KIND_LABEL[ep.kind]}</span>
				{/if}
				{#if ep.watched && ep.watched_at}
					<span>· Watched {formatDate(ep.watched_at, { year: 'numeric' })}{ep.watch_count > 1 ? ` · ${ep.watch_count}×` : ''}</span>
				{/if}
			</p>
			{#if ep.overview}
				<p class="mt-1.5 text-xs leading-relaxed text-muted {open ? '' : 'line-clamp-2 hidden sm:block'}">
					{ep.overview}
				</p>
				{#if !open}<ChevronDown class="mt-1 size-3.5 text-faint sm:hidden" />{/if}
			{/if}
		</button>
		{#if open && ep.watched && onrewatch}
			<button class="mt-2 text-xs text-muted underline-offset-2 hover:text-ink hover:underline" onclick={onrewatch}>
				Log a rewatch
			</button>
		{/if}
	</div>
	<button
		onclick={ontoggle}
		disabled={busy}
		class="grid size-9 shrink-0 place-items-center self-center rounded-full border transition {ep.watched
			? 'border-transparent text-[#09090b]'
			: 'border-line text-faint hover:border-line-strong hover:text-ink'}"
		style={ep.watched ? `background: ${accent}` : ''}
		aria-label={ep.watched ? `Unmark episode ${ep.episode_number}` : `Mark episode ${ep.episode_number} watched`}
		aria-pressed={ep.watched}
	>
		{#if busy}<LoaderCircle class="size-4 animate-spin" />{:else}<Check class="size-4" />{/if}
	</button>
</li>
