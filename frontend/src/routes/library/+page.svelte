<script lang="ts">
	import { CalendarDays, ChevronRight, UsersRound } from '@lucide/svelte';
	import { MEDIA } from '#lib/media.ts';
	import { enabledTypes } from '#lib/settings.svelte.ts';
</script>

<svelte:head><title>Library · Encompass</title></svelte:head>

<h1 class="mb-6 text-3xl font-semibold tracking-tight">Library</h1>
<div class="grid gap-2 sm:grid-cols-2">
	{#each enabledTypes() as type (type)}
		{@const meta = MEDIA[type]}
		<a
			href="/library/{type}"
			class="flex items-center gap-3 rounded-2xl border border-line bg-surface p-4 transition hover:border-line-strong hover:bg-raised"
		>
			<span
				class="grid size-10 place-items-center rounded-xl"
				style="background: color-mix(in oklab, {meta.accent} 16%, transparent); color: {meta.accent}"
			>
				<meta.icon class="size-5" />
			</span>
			<span class="flex-1 font-medium">{meta.plural}</span>
			<ChevronRight class="size-4 text-faint" />
		</a>
	{/each}
</div>

<h2 class="mt-8 mb-3 text-xs font-semibold tracking-wider text-faint uppercase">Explore</h2>
<div class="grid gap-2 sm:grid-cols-2">
	{#each [
		{ href: '/people', label: 'People', sub: 'Actors and voice actors across your library', icon: UsersRound, tint: '#f472b6' },
		{ href: '/year', label: 'Year in review', sub: 'Your year in numbers', icon: CalendarDays, tint: '#a78bfa' }
	] as link (link.href)}
		<a
			href={link.href}
			class="flex items-center gap-3 rounded-2xl border border-line bg-surface p-4 transition hover:border-line-strong hover:bg-raised"
		>
			<span
				class="grid size-10 place-items-center rounded-xl"
				style="background: color-mix(in oklab, {link.tint} 16%, transparent); color: {link.tint}"
			>
				<link.icon class="size-5" />
			</span>
			<span class="flex-1">
				<span class="block font-medium">{link.label}</span>
				<span class="block text-xs text-faint">{link.sub}</span>
			</span>
			<ChevronRight class="size-4 text-faint" />
		</a>
	{/each}
</div>
