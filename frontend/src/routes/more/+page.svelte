<script lang="ts">
	import {
		Activity,
		CalendarDays,
		LogOut,
		Settings,
		UsersRound
	} from '@lucide/svelte';
	import { goto } from '$app/navigation';
	import { logout } from '#lib/api.ts';
	import { auth } from '#lib/auth.svelte.ts';
	import ThemeToggle from '#lib/components/ThemeToggle.svelte';
	import { net } from '#lib/offline.svelte.ts';
	import { resetSettings } from '#lib/settings.svelte.ts';
	import { ListRow } from '#lib/ui/index.ts';

	const groups = [
		{
			title: 'Account',
			links: [
				{ href: '/settings', label: 'Settings', sub: 'Media types, theme, tokens, integrations and imports', icon: Settings, tint: 'var(--color-brand)' }
			]
		},
		{
			title: 'Explore',
			links: [
				{ href: '/activity', label: 'Activity', sub: 'Everything you logged, newest first', icon: Activity, tint: 'var(--color-type-game)' },
				{ href: '/people', label: 'People', sub: 'Actors and voice actors in your library', icon: UsersRound, tint: 'var(--color-type-boardgame)' },
				{ href: '/year', label: 'Year in review', sub: 'Your year in numbers', icon: CalendarDays, tint: 'var(--color-type-anime)' }
			]
		}
	];

	async function signOut() {
		await logout();
		auth.username = null;
		resetSettings();
		goto('/login');
	}
</script>

<svelte:head><title>More · Encompass</title></svelte:head>

<header class="mb-6 flex items-center justify-between gap-3">
	<div class="flex min-w-0 items-center gap-3">
		<span class="grid size-12 shrink-0 place-items-center rounded-full bg-brand/20 text-lg font-semibold text-brand uppercase">
			{auth.username?.[0] ?? '?'}
		</span>
		<div class="min-w-0">
			<h1 class="truncate text-2xl font-semibold tracking-tight">{auth.username}</h1>
			<p class="text-sm text-muted">
				{#if net.pending > 0}{net.pending} change{net.pending === 1 ? '' : 's'} waiting to sync{:else}All changes saved{/if}
			</p>
		</div>
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

<div class="mt-6 overflow-hidden rounded-2xl border border-line bg-surface">
	<ListRow title="Sign out" onclick={signOut} chevron={false}>
		{#snippet leading()}
			<span class="grid size-10 shrink-0 place-items-center rounded-xl bg-danger/12 text-danger"><LogOut class="size-5" /></span>
		{/snippet}
	</ListRow>
</div>
