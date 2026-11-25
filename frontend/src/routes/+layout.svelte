<script lang="ts">
	import '../app.css';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { auth, refreshAuth } from '#lib/auth.svelte.ts';
	import Nav from '#lib/components/Nav.svelte';

	let { children } = $props();

	const isLogin = $derived(page.url.pathname === '/login');

	$effect(() => {
		if (!auth.ready) refreshAuth();
	});

	$effect(() => {
		if (auth.ready && !auth.username && !isLogin) goto('/login');
	});
</script>

{#if !auth.ready}
	<div class="grid min-h-dvh place-items-center">
		<img src="/icon.svg" alt="" class="size-10 animate-pulse opacity-60" />
	</div>
{:else if isLogin}
	{@render children()}
{:else if auth.username}
	<Nav />
	<main class="min-h-dvh pb-24 lg:pb-10 lg:pl-60">
		<div class="mx-auto max-w-7xl px-4 pt-6 sm:px-6 lg:px-10 lg:pt-10">
			{@render children()}
		</div>
	</main>
{/if}
