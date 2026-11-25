<script lang="ts">
	import { ListChecks } from '@lucide/svelte';
	import { Button, EmptyState } from '#lib/ui/index.ts';
</script>

<svelte:head><title>Lists · Encompass</title></svelte:head>

<!-- Placeholder: the lists pages (GET /api/v1/lists) are built in a later phase. -->
<h1 class="mb-2 text-3xl font-semibold tracking-tight">Lists</h1>
<EmptyState
	icon={ListChecks}
	title="Lists are coming soon"
	description="Custom lists of movies, shows, books and games will live here."
>
	{#snippet actions()}
		<Button href="/library" variant="primary">Browse your library</Button>
	{/snippet}
</EmptyState>
