<script lang="ts">
	import { formatDate, MEDIA, minutesLabel, relativeTime } from '#lib/media.ts';
	import type { EntryDetail, MediaType } from '#lib/types.ts';
	import { Button } from '#lib/ui/index.ts';

	/** Watches/plays (episodes, with dates) and the change log of an entry, newest first. */
	let { entry, kind }: { entry: EntryDetail; kind: MediaType } = $props();

	let all = $state(false);
	const LIMIT = 6;

	interface Row {
		at: string;
		text: string;
		sub?: string;
	}

	const rows = $derived.by<Row[]>(() => {
		const out: Row[] = [];
		for (const s of entry.seasons ?? []) {
			for (const e of s.episodes) {
				if (!e.watched_at) continue;
				out.push({
					at: e.watched_at,
					text: `Watched S${s.item.season_number ?? ''} E${e.episode_number}`,
					sub: e.title && !/^Episode \d+$/.test(e.title) ? e.title : undefined
				});
			}
		}
		for (const h of entry.history) {
			const bits: string[] = [];
			if (h.type === 'created') bits.push('Added');
			if (h.status) bits.push(h.status);
			if (h.progress)
				bits.push(kind === 'game' ? minutesLabel(h.progress) : `${h.progress}${MEDIA[kind].units ? ' ' + MEDIA[kind].units : ''}`);
			if (h.score) bits.push(`★ ${h.score}`);
			out.push({ at: h.date, text: bits.join(' · ') || 'Updated' });
		}
		return out.sort((a, b) => Date.parse(b.at) - Date.parse(a.at));
	});
</script>

{#if rows.length}
	<section>
		<h2 class="mb-2 text-[15px] font-semibold">History</h2>
		<ol class="space-y-3 border-l border-line pl-5">
			{#each all ? rows : rows.slice(0, LIMIT) as row, i (i)}
				<li class="relative text-[15px]">
					<span class="absolute top-2 -left-[1.5rem] size-2 rounded-full bg-line-strong"></span>
					<span>{row.text}</span>
					{#if row.sub}<span class="block truncate text-[13px] text-muted">{row.sub}</span>{/if}
					<span class="block text-xs text-faint" title={formatDate(row.at, { year: 'numeric' })}>{relativeTime(row.at)}</span>
				</li>
			{/each}
		</ol>
		{#if rows.length > LIMIT}
			<Button size="sm" variant="ghost" class="mt-2" onclick={() => (all = !all)}>
				{all ? 'Show less' : `Show all ${rows.length}`}
			</Button>
		{/if}
	</section>
{/if}
