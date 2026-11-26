<script lang="ts">
	import {
		Activity,
		BookOpenCheck,
		CalendarDays,
		CirclePlay,
		Ellipsis,
		LibraryBig,
		LogOut,
		Search,
		Settings,
		UsersRound
	} from '@lucide/svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { logout } from '#lib/api.ts';
	import { auth } from '#lib/auth.svelte.ts';
	import { MEDIA } from '#lib/media.ts';
	import { enabledTypes, resetSettings } from '#lib/settings.svelte.ts';
	import { net } from '#lib/offline.svelte.ts';
	import ThemeToggle from './ThemeToggle.svelte';

	const path = $derived(page.url.pathname);
	const under = (...roots: string[]) => roots.some((r) => path === r || path.startsWith(r + '/'));

	/** The five phone tabs. `match` decides which tab a (sub)route belongs to. */
	const tabs = [
		{ href: '/', label: 'Now', icon: CirclePlay, match: () => path === '/' },
		{ href: '/library', label: 'Library', icon: LibraryBig, match: () => under('/library', '/item', '/media') },
		{ href: '/search', label: 'Search', icon: Search, match: () => under('/search') },
		{ href: '/reading', label: 'Reading', icon: BookOpenCheck, match: () => under('/reading') },
		{
			href: '/more',
			label: 'More',
			icon: Ellipsis,
			match: () => under('/more', '/settings', '/activity', '/people', '/year')
		}
	];

	/** Sidebar "More" group (also the links on the More page). */
	const more = [
		{ href: '/activity', label: 'Activity', icon: Activity },
		{ href: '/people', label: 'People', icon: UsersRound },
		{ href: '/year', label: 'Year in review', icon: CalendarDays },
		{ href: '/settings', label: 'Settings', icon: Settings }
	];

	const types = $derived(enabledTypes());

	async function signOut() {
		await logout();
		auth.username = null;
		resetSettings();
		goto('/login');
	}

	/** Tapping the tab you are already on scrolls to top (or returns to the tab root). */
	function tabClick(e: MouseEvent, tab: (typeof tabs)[number]) {
		if (!tab.match()) return;
		e.preventDefault();
		if (path !== tab.href) goto(tab.href);
		else window.scrollTo({ top: 0, behavior: matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth' });
	}

	const sideLink = (active: boolean) =>
		`flex min-h-9 items-center gap-3 rounded-lg px-3 text-sm font-medium transition ${active ? 'bg-ink/8 text-ink' : 'text-muted hover:bg-ink/5 hover:text-ink'}`;
</script>

<!-- Desktop sidebar -->
<aside
	class="fixed inset-y-0 left-0 z-30 hidden w-60 flex-col overflow-y-auto border-r border-line bg-canvas/80 px-3 py-5 backdrop-blur-xl lg:flex"
	aria-label="Sidebar"
>
	<a href="/" class="mb-6 flex items-center gap-2.5 px-3">
		<img src="/icon.svg" alt="" class="size-7" />
		<span class="text-[1.0625rem] font-semibold tracking-tight">Encompass</span>
	</a>

	<nav class="space-y-0.5" aria-label="Main">
		{#each [tabs[0], tabs[2], tabs[3]] as link (link.href)}
			<a href={link.href} class={sideLink(link.match())} aria-current={link.match() ? 'page' : undefined}>
				<link.icon class="size-4" />
				{link.label}
			</a>
		{/each}
	</nav>

	<p class="mt-4 mb-1 px-3 text-[0.75rem] font-semibold tracking-wider text-faint uppercase">Library</p>
	<nav class="space-y-0.5" aria-label="Library">
		<a href="/library" class={sideLink(path === '/library')} aria-current={path === '/library' ? 'page' : undefined}>
			<LibraryBig class="size-4" /> Overview
		</a>
		{#each types as type (type)}
			{@const meta = MEDIA[type]}
			{@const href = `/library/${type}`}
			<a {href} class={sideLink(under(href))} aria-current={under(href) ? 'page' : undefined}>
				<meta.icon class="size-4" style="color: {meta.accent}" />
				{meta.plural}
			</a>
		{/each}
	</nav>

	<p class="mt-4 mb-1 px-3 text-[0.75rem] font-semibold tracking-wider text-faint uppercase">More</p>
	<nav class="space-y-0.5" aria-label="More">
		{#each more as link (link.href)}
			<a href={link.href} class={sideLink(under(link.href))} aria-current={under(link.href) ? 'page' : undefined}>
				<link.icon class="size-4" />
				{link.label}
			</a>
		{/each}
	</nav>

	<div class="sticky bottom-0 mt-auto flex items-center justify-between gap-1 bg-canvas px-3 pt-3 pb-1">
		<div class="flex min-w-0 items-center gap-2.5">
			<span class="grid size-8 shrink-0 place-items-center rounded-full bg-brand/20 text-xs font-semibold text-brand uppercase">
				{auth.username?.[0] ?? '?'}
			</span>
			<span class="truncate text-sm text-muted">{auth.username}</span>
		</div>
		<div class="flex items-center">
			<ThemeToggle />
			<button
				onclick={signOut}
				class="grid size-11 place-items-center rounded-full text-faint transition hover:text-ink"
				title="Sign out"
				aria-label="Sign out"
			>
				<LogOut class="size-4" />
			</button>
		</div>
	</div>
</aside>

<!-- Phone tab bar -->
<nav
	class="fixed inset-x-0 bottom-0 z-30 grid grid-cols-5 border-t border-line bg-canvas/90 pb-[env(safe-area-inset-bottom)] backdrop-blur-xl lg:hidden"
	aria-label="Tabs"
>
	{#each tabs as tab (tab.href)}
		{@const active = tab.match()}
		<a
			href={tab.href}
			onclick={(e) => tabClick(e, tab)}
			aria-current={active ? 'page' : undefined}
			class="relative flex h-14 flex-col items-center justify-center gap-0.5 text-[0.75rem] font-medium transition {active
				? 'text-ink'
				: 'text-faint'}"
		>
			<span
				class="grid h-7 w-12 place-items-center rounded-full transition-colors {active ? 'bg-brand/18 text-brand' : ''}"
			>
				<tab.icon class="size-[22px]" />
			</span>
			{tab.label}
			{#if tab.href === '/more' && net.pending > 0}
				<span class="absolute top-1.5 left-1/2 ml-3 size-2 rounded-full bg-warning" aria-label="{net.pending} changes pending"></span>
			{/if}
		</a>
	{/each}
</nav>
