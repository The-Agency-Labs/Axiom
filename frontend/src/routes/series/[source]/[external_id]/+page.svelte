<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { get } from '#lib/api.ts';
	import type { SeriesDetail } from '#lib/components/reading/types.ts';
	import { Button, EmptyState, Skeleton } from '#lib/ui/index.ts';

	// A series found by search but not stored yet: fetching it stores it, then we
	// move to its permanent page.
	let error = $state('');
	$effect(() => {
		const { source, external_id } = page.params;
		error = '';
		get<SeriesDetail>(`/series/${source}/${external_id}`)
			.then((s) => goto(`/series/${s.id}`, { replaceState: true }))
			.catch(() => (error = 'Could not load this series from the provider.'));
	});
</script>

<svelte:head><title>Series · Axiom</title></svelte:head>

{#if error}
	<EmptyState title="Series unavailable" description={error}>
		{#snippet actions()}<Button href="/series">Back to series</Button>{/snippet}
	</EmptyState>
{:else}
	<div class="space-y-3" aria-hidden="true"><Skeleton class="h-8 w-48" /><Skeleton class="h-64 w-full" /></div>
	<p class="mt-3 text-center text-sm text-muted">Fetching series…</p>
{/if}
