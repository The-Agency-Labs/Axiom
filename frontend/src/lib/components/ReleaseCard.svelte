<script lang="ts">
	import Cover from '#lib/components/Cover.svelte';
	import type { CalendarEvent } from '#lib/components/reading/types.ts';
	import { MEDIA, titleHref } from '#lib/media.ts';

	/** One upcoming release, laid out like a card on Now. `when` is its date, where the section doesn't say it. */
	let { event, when = '' }: { event: CalendarEvent; when?: string } = $props();

	const meta = $derived(MEDIA[event.media_type]);
	const what = $derived.by(() => {
		if (event.season !== null && event.episode !== null)
			return event.season === 0 ? `Special ${event.episode}` : `Season ${event.season} · Episode ${event.episode}`;
		if (event.episode !== null) return `${meta.unit.charAt(0).toUpperCase()}${meta.unit.slice(1)} ${event.episode}`;
		return 'Release';
	});
	// Unaired episodes are often just named "Episode 12", which the line above already says.
	const title = $derived(/^episode \d+$/i.test(event.episode_title) ? '' : event.episode_title);
	const time = $derived(
		event.all_day || !event.time ? '' : new Date(event.time).toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' })
	);
</script>

<article class="min-w-0 rounded-[var(--radius-card)] border border-line bg-surface p-3">
	<a href={titleHref(event)} class="flex gap-3 rounded-xl" aria-label={when ? `${event.title}, ${when}` : event.title}>
		<Cover src={event.image} title={event.name} type={event.media_type} class="aspect-[2/3] w-16 shrink-0 sm:w-20" />
		<span class="flex min-w-0 flex-1 flex-col py-0.5">
			<span class="line-clamp-2 text-[0.9375rem] leading-snug font-semibold">{event.name}</span>
			<span class="mt-0.5 line-clamp-1 text-[0.8125rem] text-muted">{what}</span>
			{#if title}<span class="line-clamp-1 text-[0.8125rem] text-faint">{title}</span>{/if}
			<span class="mt-auto flex items-center gap-1.5 pt-2 text-xs font-medium">
				<meta.icon class="size-3.5" style="color: {meta.accent}" />
				<span class="min-w-0 truncate">{[when, time].filter(Boolean).join(' · ') || meta.label}</span>
			</span>
		</span>
	</a>
</article>
