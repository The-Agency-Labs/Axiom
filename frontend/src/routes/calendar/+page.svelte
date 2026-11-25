<script lang="ts">
	import { CalendarRange } from '@lucide/svelte';
	import { Button, EmptyState } from '#lib/ui/index.ts';
</script>

<svelte:head><title>Calendar · Encompass</title></svelte:head>

<!-- Placeholder: the calendar page (GET /api/v1/calendar) is built in a later phase. -->
<h1 class="mb-2 text-3xl font-semibold tracking-tight">Calendar</h1>
<EmptyState
	icon={CalendarRange}
	title="Calendar is coming soon"
	description="Upcoming episodes and releases will show up here. You can already subscribe to the calendar feed from Settings."
>
	{#snippet actions()}
		<Button href="/settings#integrations" variant="primary">Open Settings</Button>
	{/snippet}
</EmptyState>
