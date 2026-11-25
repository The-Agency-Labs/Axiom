<script lang="ts">
	import { Check, LoaderCircle, Plus } from '@lucide/svelte';
	import { post } from '#lib/api.ts';
	import { MEDIA, progressLabel, relativeTime } from '#lib/media.ts';
	import type { NowEntry } from '#lib/types.ts';
	import Cover from './Cover.svelte';
	import ProgressBar from '#lib/ui/ProgressBar.svelte';

	let { entry, onchange }: { entry: NowEntry; onchange?: () => void } = $props();

	const meta = $derived(MEDIA[entry.media_type]);
	const href = $derived(`/item/${entry.media_type}/${entry.id}`);
	let busy = $state(false);
	let progress = $derived(entry.progress);
	const percent = $derived(entry.total ? (progress / entry.total) * 100 : null);

	const subtitle = $derived.by(() => {
		if (entry.media_type === 'season') {
			const next = entry.next;
			if (next) return `S${entry.season_number} · Next E${next.number}${next.title ? ` · ${next.title}` : ''}`;
			return `Season ${entry.season_number}`;
		}
		if (entry.next) return `Next ${meta.unit} ${entry.next.number}`;
		if (entry.last_session)
			return `Last read ${relativeTime(entry.last_session.started_at)} · ${entry.last_session.pages_read} pages`;
		return entry.genres?.slice(0, 2).join(' · ') ?? '';
	});

	async function step(event: MouseEvent) {
		event.preventDefault();
		event.stopPropagation();
		busy = true;
		try {
			const updated = await post<{ progress: number }>(
				`/library/${entry.media_type}/${entry.id}/step`,
				{ direction: 'up' }
			);
			progress = updated.progress;
			onchange?.();
		} finally {
			busy = false;
		}
	}
</script>

<a
	{href}
	class="group relative flex w-[19rem] shrink-0 gap-3.5 rounded-2xl border border-line bg-surface p-3 transition hover:border-line-strong hover:bg-raised sm:w-[22rem]"
	style="--accent: {meta.accent}"
>
	<Cover src={entry.image} title={entry.title} type={entry.media_type} class="aspect-[2/3] w-20 shrink-0 sm:w-24" />

	<div class="flex min-w-0 flex-1 flex-col py-0.5">
		<h3 class="line-clamp-2 text-[15px] leading-snug font-semibold">{entry.title}</h3>
		<p class="mt-1 line-clamp-2 text-xs leading-relaxed text-muted">{subtitle}</p>

		<div class="mt-auto space-y-2 pt-3">
			<div class="flex items-baseline justify-between text-xs">
				<span class="font-medium tabular-nums text-ink/90">
					{progressLabel(entry.media_type, progress, entry.total)}
				</span>
				{#if percent !== null}
					<span class="tabular-nums text-faint">{Math.round(percent)}%</span>
				{/if}
			</div>
			<ProgressBar {percent} accent={meta.accent} />
		</div>
	</div>

	{#if meta.steppable}
		<button
			type="button"
			onclick={step}
			disabled={busy || (entry.total !== null && progress >= entry.total)}
			class="absolute top-3 right-3 grid size-8 place-items-center rounded-full border border-line bg-canvas/70 text-muted opacity-100 backdrop-blur transition hover:border-[var(--accent)] hover:text-[var(--accent)] disabled:opacity-40 sm:opacity-0 sm:group-hover:opacity-100"
			title="+1 {meta.unit}"
			aria-label="Add one {meta.unit}"
		>
			{#if busy}
				<LoaderCircle class="size-4 animate-spin" />
			{:else if entry.total !== null && progress >= entry.total}
				<Check class="size-4" />
			{:else}
				<Plus class="size-4" />
			{/if}
		</button>
	{/if}
</a>
