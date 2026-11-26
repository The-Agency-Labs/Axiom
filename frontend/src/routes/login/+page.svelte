<script lang="ts">
	import { goto } from '$app/navigation';
	import { ApiError, login } from '#lib/api.ts';
	import { auth } from '#lib/auth.svelte.ts';

	let username = $state('');
	let password = $state('');
	let error = $state('');
	let busy = $state(false);

	async function submit(event: SubmitEvent) {
		event.preventDefault();
		busy = true;
		error = '';
		try {
			const data = await login(username, password);
			auth.username = data.username;
			// Legacy Django pages (settings, imports) send people here with ?next=.
			const next = new URLSearchParams(location.search).get('next');
			if (next && next.startsWith('/') && !next.startsWith('//')) {
				location.href = next;
			} else {
				goto('/');
			}
		} catch (e) {
			error = e instanceof ApiError && e.status === 401 ? 'Wrong username or password.' : 'Could not sign in.';
		} finally {
			busy = false;
		}
	}
</script>

<svelte:head><title>Sign in · Axiom</title></svelte:head>

<div class="relative grid min-h-dvh place-items-center overflow-hidden px-4">
	<div
		class="pointer-events-none absolute -top-40 left-1/2 h-[32rem] w-[48rem] -translate-x-1/2 rounded-full opacity-40 blur-3xl"
		style="background: radial-gradient(closest-side, #8b9cff55, transparent)"
	></div>

	<form onsubmit={submit} class="relative w-full max-w-sm space-y-5">
		<div class="mb-8 flex flex-col items-center gap-3 text-center">
			<img src="/icon.svg" alt="" class="size-12" />
			<h1 class="text-2xl font-semibold tracking-tight">Axiom</h1>
			<p class="text-sm text-muted">Everything you watch and read, in one place.</p>
		</div>

		<label class="block space-y-1.5">
			<span class="text-xs font-medium text-muted">Username</span>
			<input
				bind:value={username}
				autocomplete="username"
				required
				class="w-full rounded-xl border border-line bg-surface px-3.5 py-2.5 text-sm outline-none transition focus:border-brand/60 focus:ring-4 focus:ring-brand/10"
			/>
		</label>
		<label class="block space-y-1.5">
			<span class="text-xs font-medium text-muted">Password</span>
			<input
				type="password"
				bind:value={password}
				autocomplete="current-password"
				required
				class="w-full rounded-xl border border-line bg-surface px-3.5 py-2.5 text-sm outline-none transition focus:border-brand/60 focus:ring-4 focus:ring-brand/10"
			/>
		</label>

		{#if error}
			<p class="text-sm text-rose-400">{error}</p>
		{/if}

		<button
			disabled={busy}
			class="w-full rounded-xl bg-ink py-2.5 text-sm font-semibold text-canvas transition hover:bg-white disabled:opacity-60"
		>
			{busy ? 'Signing in…' : 'Sign in'}
		</button>
	</form>
</div>
