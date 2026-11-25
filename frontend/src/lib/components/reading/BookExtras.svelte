<script lang="ts">
	// Book-only sections on the title page: the series strip and Triple P + Reflect.
	// Contract (kept from the stub): the title page renders this for books (kind === 'book').
	//   item:    the item payload (needs `id`, `media_id`, optional `series: {id, name, position}`);
	//            an entry-shaped object with `status` / `item` inside is also understood.
	//   entryId: the viewer's entry id, or null when the book is not tracked (no notes then).
	import { get } from '#lib/api.ts';
	import SeriesStrip from './SeriesStrip.svelte';
	import TriplePCard from './TriplePCard.svelte';

	let { item, entryId }: { item: Record<string, unknown>; entryId: number | null } = $props();

	// Accept either an item or an entry-shaped object.
	const inner = $derived((item.item as Record<string, unknown> | undefined) ?? item);
	const series = $derived((inner.series ?? item.series ?? null) as { id: number; name: string; position: number } | null);
	const itemId = $derived((inner.id as number | undefined) ?? null);
	const mediaId = $derived((inner.media_id as string | undefined) ?? null);

	// Reflect opens by itself once the book is Completed.
	let fetched = $state<string | null>(null);
	const status = $derived((item.status as string | undefined) ?? fetched);
	$effect(() => {
		const id = entryId;
		if (id == null || item.status) return;
		get<{ status: string }>(`/library/book/${id}?include_metadata=false`)
			.then((e) => id === entryId && (fetched = e.status))
			.catch(() => {});
	});
</script>

{#if series}
	<SeriesStrip seriesId={series.id} currentId={itemId} currentMediaId={mediaId} />
{/if}
{#if entryId != null}
	<TriplePCard {entryId} completed={status === 'Completed'} />
{/if}
