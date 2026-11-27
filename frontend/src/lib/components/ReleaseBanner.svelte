<script lang="ts">
	import Cover from '#lib/components/Cover.svelte';
	import type { CalendarEvent } from '#lib/components/reading/types.ts';
	import { MEDIA, titleHref } from '#lib/media.ts';

	/** One release in the agenda, wide, over the title's banner art. */
	let { event }: { event: CalendarEvent } = $props();

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
	let failed = $state(false);
	const art = $derived(!!event.backdrop && !failed);
</script>

<a
	href={titleHref(event)}
	class="group relative isolate flex h-40 min-w-0 items-end overflow-hidden rounded-[var(--radius-card)] border border-line bg-surface sm:h-52"
	aria-label={event.title}
>
	{#if art}
		<img
			src={event.backdrop}
			alt=""
			loading="lazy"
			decoding="async"
			class="absolute inset-0 -z-20 size-full object-cover object-[center_25%] transition duration-300 group-hover:scale-[1.02]"
			onerror={() => (failed = true)}
		/>
	{:else if event.image}
		<!-- No banner: the poster, blurred, stands in. -->
		<img src={event.image} alt="" loading="lazy" class="absolute inset-0 -z-20 size-full scale-110 object-cover blur-2xl" />
	{/if}
	<!-- Always dark under the white text, whatever the art or theme. -->
	<span class="absolute inset-0 -z-10 bg-gradient-to-t from-black/90 via-black/65 to-black/15 sm:bg-gradient-to-r sm:from-black/90 sm:via-black/50 sm:to-transparent"></span>
	<span class="flex w-full min-w-0 items-end gap-3 p-3 text-white sm:p-4">
		{#if !art}<Cover src={event.image} title={event.name} type={event.media_type} class="aspect-[2/3] w-16 shrink-0 sm:w-24" />{/if}
		<span class="min-w-0 flex-1">
			<span class="line-clamp-2 text-base leading-snug font-semibold sm:text-xl">{event.name}</span>
			<span class="mt-0.5 line-clamp-1 text-[0.8125rem] text-white/85">{what}</span>
			{#if title}<span class="line-clamp-1 text-[0.8125rem] text-white/65">{title}</span>{/if}
			<span class="mt-1.5 flex items-center gap-1.5 text-xs font-medium text-white/80">
				<meta.icon class="size-3.5" />{[meta.label, time].filter(Boolean).join(' · ')}
			</span>
		</span>
	</span>
</a>
