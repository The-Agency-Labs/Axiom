<script lang="ts">
	// Book-only sections on the title page: Triple P + Reflect and the series
	// strip. Owned by the reading UI work; the title page just renders this
	// for books (kind === 'book') with the viewer's entry id, if tracked.
	let { item, entryId }: { item: Record<string, unknown>; entryId: number | null } = $props();
</script>
