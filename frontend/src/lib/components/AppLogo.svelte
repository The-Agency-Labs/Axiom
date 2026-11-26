<script lang="ts">
	/** The logo of an app that tracks titles (Jellyfin, Kindle, Paperback, Steam); its name when there is none. */
	let { name, class: className = 'h-5' }: { name: string; class?: string } = $props();

	const ICONS: Record<string, string> = {
		Jellyfin: '/apps/jellyfin.svg',
		Kindle: '/apps/kindle.png',
		Paperback: '/apps/paperback.svg',
		Steam: '/apps/steam.svg'
	};
</script>

{#if ICONS[name]}
	<img src={ICONS[name]} alt={name} title={name} class="inline-block aspect-square w-auto shrink-0 rounded-[22%] object-contain {className}" />
{:else}
	<span class="text-xs font-medium">{name}</span>
{/if}
