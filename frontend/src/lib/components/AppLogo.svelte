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
	<!-- The rounding is for the square app icons. Jellyfin's mark carries its own margin in
	     the file, so it matches their size and no edge of it is cut off. -->
	<img
		src={ICONS[name]}
		alt={name}
		title={name}
		class="inline-block aspect-square w-auto shrink-0 object-contain {name === 'Jellyfin' ? '' : 'rounded-[22%]'} {className}"
	/>
{:else}
	<span class="text-xs font-medium">{name}</span>
{/if}
