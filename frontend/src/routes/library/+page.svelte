<script lang="ts">
	import { CalendarDays, UsersRound } from '@lucide/svelte';
	import { get, qs } from '#lib/api.ts';
	import { MEDIA } from '#lib/media.ts';
	import { enabledTypes } from '#lib/settings.svelte.ts';
	import type { LibraryResponse, MediaType } from '#lib/types.ts';
	import { Card, ListRow, Skeleton } from '#lib/ui/index.ts';

	let counts = $state<Record<string, { all: number; active: number } | undefined>>({});

	const types = $derived(enabledTypes());

	$effect(() => {
		for (const t of types) {
			if (counts[t]) continue;
			Promise.all([
				get<LibraryResponse>('/library' + qs({ media_type: t, limit: 1 })),
				get<LibraryResponse>('/library' + qs({ media_type: t, status: 'In progress', limit: 1 }))
			])
				.then(([all, active]) => (counts[t] = { all: all.count, active: active.count }))
				.catch(() => {});
		}
	});

	const explore = [
		{ href: '/people', label: 'People', sub: 'Actors and voice actors across your library', icon: UsersRound, tint: 'var(--color-type-boardgame)' },
		{ href: '/year', label: 'Year in review', sub: 'Your year in numbers', icon: CalendarDays, tint: 'var(--color-type-anime)' }
	];
</script>

<svelte:head><title>Library · Axiom</title></svelte:head>

<h1 class="mb-4 text-2xl font-semibold tracking-tight sm:text-3xl">Library</h1>

<Card flush>
	<ul class="divide-y divide-line">
		{#each types as type (type)}
			{@const meta = MEDIA[type as MediaType]}
			{@const c = counts[type]}
			<li>
				<ListRow
					href="/library/{type}"
					title={meta.plural}
					subtitle={c ? `${c.all} tracked${c.active ? ` · ${c.active} in progress` : ''}` : ' '}
				>
					{#snippet leading()}
						<span
							class="grid size-10 shrink-0 place-items-center rounded-xl"
							style="background: color-mix(in oklab, {meta.accent} 16%, transparent); color: {meta.accent}"
						>
							<meta.icon class="size-5" />
						</span>
					{/snippet}
					{#snippet trailing()}
						{#if c}
							<span class="text-lg font-semibold tabular-nums">{c.all}</span>
						{:else}
							<Skeleton class="h-6 w-8" />
						{/if}
					{/snippet}
				</ListRow>
			</li>
		{/each}
	</ul>
</Card>

<h2 class="mt-8 mb-2 text-xs font-semibold tracking-wider text-faint uppercase">Explore</h2>
<Card flush>
	<ul class="divide-y divide-line">
		{#each explore as link (link.href)}
			<li>
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
			</li>
		{/each}
	</ul>
</Card>
