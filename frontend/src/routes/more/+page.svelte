<script lang="ts">
	import {
		Activity,
		CalendarDays,
		CalendarRange,
		RefreshCw,
		UsersRound
	} from '@lucide/svelte';
	import ProfileMenu from '#lib/components/ProfileMenu.svelte';
	import ThemeToggle from '#lib/components/ThemeToggle.svelte';
	import { net } from '#lib/offline.svelte.ts';
	import { ListRow } from '#lib/ui/index.ts';

	const groups = [
		{
			title: 'Account',
			links: [
				{ href: '/sources', label: 'Sources', sub: 'Jellyfin, AniList, Goodreads, Steam', icon: RefreshCw, tint: 'var(--color-type-tv)' }
			]
		},
		{
			title: 'Explore',
			links: [
				{ href: '/activity', label: 'Activity', sub: 'Everything you logged, newest first', icon: Activity, tint: 'var(--color-type-game)' },
				{ href: '/calendar', label: 'Calendar', sub: 'Upcoming episodes and releases', icon: CalendarRange, tint: 'var(--color-type-manga)' },
				{ href: '/people', label: 'People', sub: 'Actors and voice actors in your library', icon: UsersRound, tint: 'var(--color-type-boardgame)' },
				{ href: '/year', label: 'Year in review', sub: 'Your year in numbers', icon: CalendarDays, tint: 'var(--color-type-anime)' }
			]
		}
	];
</script>

<svelte:head><title>More · Axiom</title></svelte:head>

<header class="mb-6 flex items-center justify-between gap-3">
	<div class="min-w-0 flex-1">
		<ProfileMenu class="max-w-72" />
		<p class="mt-1 px-2 text-sm text-muted">
			{#if net.pending > 0}{net.pending} change{net.pending === 1 ? '' : 's'} waiting to sync{:else}All changes saved{/if}
		</p>
	</div>
	<ThemeToggle />
</header>

{#each groups as group (group.title)}
	<h2 class="mt-6 mb-2 px-1 text-xs font-semibold tracking-wider text-faint uppercase">{group.title}</h2>
	<div class="divide-y divide-line overflow-hidden rounded-2xl border border-line bg-surface">
		{#each group.links as link (link.href)}
			<ListRow href={link.href} title={link.label} subtitle={link.sub}>
				{#snippet leading()}
					<span
						class="grid size-10 shrink-0 place-items-center rounded-xl"
						style="background: color-mix(in oklab, {link.tint} 16%, transparent); color: {link.tint}"
					>
						<link.icon class="size-5" />
					</span>
				{/snippet}
			</ListRow>
		{/each}
	</div>
{/each}
