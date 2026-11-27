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
	<!-- Jellyfin's mark runs to the edges, so in the same box it looks bigger than the
	     rounded app icons: it is drawn a little smaller to match them. -->
	<img
		src={ICONS[name]}
		alt={name}
		title={name}
		class="inline-block aspect-square w-auto shrink-0 rounded-[22%] object-contain {className} {name === 'Jellyfin' ? 'scale-90' : ''}"
	/>
{:else}
	<span class="text-xs font-medium">{name}</span>
{/if}
