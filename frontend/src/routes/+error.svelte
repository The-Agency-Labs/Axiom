<script lang="ts">
	import { page } from '$app/state';
	import { CircleAlert, Compass } from '@lucide/svelte';
	import { Button, EmptyState } from '#lib/ui/index.ts';

	const missing = $derived(page.status === 404);
</script>

<svelte:head><title>{missing ? 'Not found' : 'Something went wrong'} · Axiom</title></svelte:head>

<EmptyState
	icon={missing ? Compass : CircleAlert}
	title={missing ? 'Nothing here' : 'Something went wrong'}
	description={missing
		? 'This page doesn’t exist, or it was removed.'
		: (page.error?.message ?? 'Try again in a moment.')}
	class="min-h-[60vh] justify-center"
>
	{#snippet actions()}
		<Button href="/" variant="primary">Go to Now</Button>
		<Button href="/library">Library</Button>
	{/snippet}
</EmptyState>
