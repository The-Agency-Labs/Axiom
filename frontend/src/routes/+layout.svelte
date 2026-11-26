<script lang="ts">
	import '../app.css';
	import { afterNavigate, beforeNavigate, goto } from '$app/navigation';
	import { page } from '$app/state';
	import { tick } from 'svelte';
	import { auth, refreshAuth } from '#lib/auth.svelte.ts';
	import Nav from '#lib/components/Nav.svelte';
	import TopBar from '#lib/components/TopBar.svelte';
	import { initOffline } from '#lib/offline.svelte.ts';
	import { loadSettings } from '#lib/settings.svelte.ts';
	import { initTheme } from '#lib/theme.svelte.ts';
	import OfflineBanner from '#lib/ui/OfflineBanner.svelte';
	import Toast from '#lib/ui/Toast.svelte';

	let { children } = $props();

	const isLogin = $derived(page.url.pathname === '/login');

	$effect(() => {
		initTheme();
		initOffline();
	});

	$effect(() => {
		if (!auth.ready) refreshAuth();
	});

	$effect(() => {
		if (auth.ready && !auth.username && !isLogin) goto('/login');
	});

	$effect(() => {
		if (auth.username) loadSettings();
	});

	// --- Per-tab scroll restoration -------------------------------------------------------------
	// Back/forward is handled by SvelteKit. Switching between the tab roots is a fresh navigation,
	// so remember where each tab root was scrolled and put it back when the user returns to it.
	const TAB_ROOTS = new Set(['/', '/library', '/search', '/reading', '/more']);
	const tabScroll = new Map<string, number>();

	beforeNavigate(({ from, to }) => {
		const here = from?.url.pathname;
		if (here && TAB_ROOTS.has(here) && to?.url.pathname !== here) tabScroll.set(here, window.scrollY);
	});

	afterNavigate(async ({ to, type }) => {
		const dest = to?.url.pathname;
		if (!dest || !TAB_ROOTS.has(dest) || type === 'popstate') return;
		const y = tabScroll.get(dest);
		if (!y) return;
		// Content loads asynchronously: wait (briefly) until the page is tall enough.
		await tick();
		for (let i = 0; i < 20; i++) {
			if (document.documentElement.scrollHeight - window.innerHeight >= y) break;
			await new Promise((r) => setTimeout(r, 50));
		}
		window.scrollTo({ top: y, behavior: 'instant' });
	});
</script>

{#if !auth.ready}
	<div class="grid min-h-dvh place-items-center">
		<img src="/icon.svg" alt="" class="size-10 animate-pulse opacity-60" />
	</div>
{:else if isLogin}
	{@render children()}
{:else if auth.username}
	<a
		href="#main"
		class="sr-only focus:not-sr-only focus:fixed focus:top-2 focus:left-2 focus:z-[80] focus:rounded-lg focus:bg-brand focus:px-3 focus:py-2 focus:text-on-brand"
		>Skip to content</a
	>
	<Nav />
	<div class="pt-[env(safe-area-inset-top)] lg:pl-60">
		<TopBar />
		<OfflineBanner />
		<main
			id="main"
			class="min-h-dvh pb-[calc(var(--spacing-tabbar)+env(safe-area-inset-bottom)+1.5rem)] lg:pb-10"
		>
			<div class="mx-auto max-w-7xl px-4 pt-5 sm:px-6 lg:px-10 lg:pt-8">
				{#key page.route.id}
					<div class="page-in">
						{@render children()}
					</div>
				{/key}
			</div>
		</main>
	</div>
{/if}

<Toast />
