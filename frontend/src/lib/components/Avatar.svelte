<script lang="ts">
	import { auth } from '#lib/auth.svelte.ts';
	import { settings, shownName } from '#lib/settings.svelte.ts';

	/** The signed-in user's picture, or their initial when they haven't set one. */
	let { class: className = 'size-8 text-xs' }: { class?: string } = $props();
	const picture = $derived(settings.data?.avatar);
	const initial = $derived(shownName(auth.username)[0] ?? '?');
</script>

{#if picture}
	<img src={picture} alt="" class="shrink-0 rounded-full object-cover {className}" />
{:else}
	<span class="grid shrink-0 place-items-center rounded-full bg-brand/20 font-semibold text-brand uppercase {className}">{initial}</span>
{/if}
