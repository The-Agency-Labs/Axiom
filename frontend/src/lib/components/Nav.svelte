<script lang="ts">
	import { Activity, BookOpenCheck, CirclePlay, LibraryBig, LogOut, Search } from '@lucide/svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { logout } from '#lib/api.ts';
	import { auth } from '#lib/auth.svelte.ts';
	import { LIBRARY_TYPES, MEDIA } from '#lib/media.ts';

	const path = $derived(page.url.pathname);
	const isActive = (href: string) => (href === '/' ? path === '/' : path.startsWith(href));

	const primary = [
		{ href: '/', label: 'Now', icon: CirclePlay },
		{ href: '/search', label: 'Search', icon: Search },
		{ href: '/reading', label: 'Reading', icon: BookOpenCheck },
		{ href: '/activity', label: 'Activity', icon: Activity }
	];

	async function signOut() {
		await logout();
		auth.username = null;
		goto('/login');
	}
</script>

<!-- Desktop sidebar -->
<aside
	class="fixed inset-y-0 left-0 z-30 hidden w-60 flex-col border-r border-line bg-canvas/80 px-3 py-5 backdrop-blur-xl lg:flex"
>
	<a href="/" class="mb-7 flex items-center gap-2.5 px-3">
		<img src="/icon.svg" alt="" class="size-7" />
		<span class="text-[17px] font-semibold tracking-tight">Encompass</span>
	</a>

	<nav class="space-y-0.5">
		{#each primary as link (link.href)}
			<a
				href={link.href}
				class="flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium transition {isActive(link.href)
					? 'bg-white/8 text-ink'
					: 'text-muted hover:bg-white/4 hover:text-ink'}"
			>
				<link.icon class="size-4" />
				{link.label}
			</a>
		{/each}
	</nav>

	<p class="mt-7 mb-2 px-3 text-[11px] font-semibold tracking-wider text-faint uppercase">Library</p>
	<nav class="space-y-0.5">
		{#each LIBRARY_TYPES as type (type)}
			{@const meta = MEDIA[type]}
			{@const href = `/library/${type}`}
			<a
				{href}
				class="flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium transition {isActive(href)
					? 'bg-white/8 text-ink'
					: 'text-muted hover:bg-white/4 hover:text-ink'}"
			>
				<meta.icon class="size-4" style="color: {meta.accent}" />
				{meta.plural}
			</a>
		{/each}
	</nav>

	<div class="mt-auto flex items-center justify-between rounded-lg px-3 py-2">
		<div class="flex items-center gap-2.5">
			<span class="grid size-7 place-items-center rounded-full bg-brand/20 text-xs font-semibold text-brand uppercase">
				{auth.username?.[0] ?? '?'}
			</span>
			<span class="text-sm text-muted">{auth.username}</span>
		</div>
		<button onclick={signOut} class="text-faint transition hover:text-ink" title="Sign out" aria-label="Sign out">
			<LogOut class="size-4" />
		</button>
	</div>
</aside>

<!-- Mobile tab bar -->
<nav
	class="pb-safe fixed inset-x-0 bottom-0 z-30 grid grid-cols-5 border-t border-line bg-canvas/85 px-2 pt-1.5 backdrop-blur-xl lg:hidden"
>
	{#each [primary[0], { href: '/library', label: 'Library', icon: LibraryBig }, primary[1], primary[2], primary[3]] as link (link.href)}
		<a
			href={link.href}
			class="flex flex-col items-center gap-1 rounded-lg py-1.5 text-[10px] font-medium transition {isActive(link.href)
				? 'text-ink'
				: 'text-faint'}"
		>
			<link.icon class="size-5" />
			{link.label}
		</a>
	{/each}
</nav>
