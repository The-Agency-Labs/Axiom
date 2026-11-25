<script lang="ts">
	import { ChevronDown, CircleAlert, CircleCheck, CircleDashed } from '@lucide/svelte';
	import type { Component, Snippet } from 'svelte';
	import { relativeTime } from '#lib/media.ts';

	export interface SourceStatus {
		last_run?: string;
		ok: boolean;
		counts?: Record<string, number>;
		failures?: string[];
		message?: string;
		// Jellyfin webhook only
		last_event?: string;
		last_item?: string;
	}

	/** One source (Jellyfin, AniList, ...): setup state, last run, counts, expandable failures, actions. */
	let {
		title,
		subtitle,
		icon: Icon,
		accent,
		configured,
		status,
		hint,
		actions,
		extra
	}: {
		title: string;
		subtitle: string;
		icon: Component<any>;
		accent: string;
		configured: boolean;
		status: SourceStatus | null;
		/** Shown when the source is not configured: how to set it up. */
		hint?: string;
		actions?: Snippet;
		/** Extra content below the status (e.g. the dry-run preview). */
		extra?: Snippet;
	} = $props();

	let open = $state(false);
	const when = $derived(status?.last_run ?? status?.last_event);
	const failures = $derived(status?.failures ?? []);
	const counts = $derived(Object.entries(status?.counts ?? {}).filter(([, n]) => n > 0));
</script>

<section class="rounded-[var(--radius-card)] border border-line bg-surface p-4">
	<header class="flex items-start gap-3">
		<span
			class="grid size-10 shrink-0 place-items-center rounded-xl"
			style="background: color-mix(in oklab, {accent} 16%, transparent); color: {accent}"
		>
			<Icon class="size-5" />
		</span>
		<div class="min-w-0 flex-1">
			<h2 class="text-[15px] font-semibold">{title}</h2>
			<p class="text-[13px] text-muted">{subtitle}</p>
		</div>
		{#if !configured}
			<span class="inline-flex items-center gap-1.5 rounded-full border border-line px-2.5 py-1 text-xs font-medium text-muted">
				<CircleDashed class="size-3.5" /> Not set up
			</span>
		{:else if !status}
			<span class="inline-flex items-center gap-1.5 rounded-full border border-line px-2.5 py-1 text-xs font-medium text-muted">
				<CircleDashed class="size-3.5" /> Never run
			</span>
		{:else if status.ok}
			<span class="inline-flex items-center gap-1.5 rounded-full border border-success/40 bg-success/10 px-2.5 py-1 text-xs font-medium text-success">
				<CircleCheck class="size-3.5" /> OK
			</span>
		{:else}
			<span class="inline-flex items-center gap-1.5 rounded-full border border-danger/40 bg-danger/10 px-2.5 py-1 text-xs font-medium text-danger">
				<CircleAlert class="size-3.5" /> Failed
			</span>
		{/if}
	</header>

	{#if !configured && hint}
		<p class="mt-3 rounded-xl bg-raised px-3 py-2 text-[13px] text-muted">{hint}</p>
	{/if}

	{#if status}
		<div class="mt-3 text-[13px] text-muted">
			{#if status.last_event}
				Last event <span class="text-ink">{relativeTime(status.last_event)}</span>{#if status.last_item}
					: <span class="text-ink">{status.last_item}</span>{/if}
			{:else if when}
				Last run <span class="text-ink">{relativeTime(when)}</span>
			{/if}
			{#if status.message}<p class="mt-1 {status.ok ? '' : 'text-danger'}">{status.message}</p>{/if}
		</div>
		{#if counts.length}
			<ul class="mt-2 flex flex-wrap gap-1.5">
				{#each counts as [name, n] (name)}
					<li class="rounded-full border border-line bg-raised px-2.5 py-0.5 text-xs text-muted">
						<span class="font-semibold text-ink tabular-nums">{n}</span> {name.replaceAll('_', ' ')}
					</li>
				{/each}
			</ul>
		{/if}
		{#if failures.length}
			<button
				type="button"
				class="mt-2 flex min-h-11 w-full items-center justify-between rounded-xl text-left text-sm font-medium text-danger"
				aria-expanded={open}
				onclick={() => (open = !open)}
			>
				{failures.length} problem{failures.length === 1 ? '' : 's'}
				<ChevronDown class="size-4 transition-transform {open ? 'rotate-180' : ''}" />
			</button>
			{#if open}
				<ul class="max-h-64 space-y-1 overflow-y-auto rounded-xl bg-raised p-3 text-xs break-words text-muted">
					{#each failures as failure, i (i)}<li>{failure}</li>{/each}
				</ul>
			{/if}
		{/if}
	{/if}

	{@render extra?.()}

	{#if actions && configured}
		<div class="mt-3 flex flex-wrap gap-2">{@render actions()}</div>
	{/if}
</section>
