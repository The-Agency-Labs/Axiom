<script lang="ts">
	import { BookmarkPlus, Check, ChevronDown, Clock, Play, Star } from '@lucide/svelte';
	import { STATUS_COLOR } from '#lib/media.ts';
	import type { Status } from '#lib/types.ts';
	import { Button, Stepper } from '#lib/ui/index.ts';

	/**
	 * The title page's primary actions, pinned above the tab bar on phones (thumb reach) and inline on
	 * desktop. Tracked: status, score and the kind's progress control. Untracked: Track buttons.
	 */
	let {
		tracked,
		status,
		score,
		mode,
		next,
		step,
		film,
		track,
		onstatus,
		onscore,
		onnext,
		onstep,
		onwatched,
		ontrack
	}: {
		tracked: boolean;
		status: Status;
		score: number | null;
		/** episodes: "mark next"; steps: stepper; film: watched button; none. */
		mode: 'episodes' | 'steps' | 'film' | 'none';
		next?: { label: string; disabled: boolean; upcoming?: boolean } | null;
		step?: { display: string; short: string; label: string; canDecrement: boolean; canIncrement: boolean };
		film?: { done: boolean };
		track: { primary: [Status, string]; secondary: [Status, string] };
		onstatus: () => void;
		onscore: () => void;
		onnext: () => void;
		onstep: (direction: 1 | -1) => void;
		onwatched: () => void;
		ontrack: (status: Status) => void;
	} = $props();
</script>

<div
	class="fixed inset-x-0 bottom-[calc(var(--spacing-tabbar)+env(safe-area-inset-bottom))] z-30 border-t border-line bg-canvas/90 px-3 py-2 backdrop-blur-xl lg:static lg:mt-5 lg:border-0 lg:bg-transparent lg:p-0 lg:backdrop-blur-none"
>
	<div class="mx-auto flex max-w-xl flex-wrap items-center gap-2 lg:mx-0">
		{#if !tracked}
			<Button variant="primary" class="min-w-0 flex-1" icon={track.primary[0] === 'Completed' ? Check : Play} onclick={() => ontrack(track.primary[0])}>
				{track.primary[1]}
			</Button>
			<Button class="min-w-0 flex-1" icon={BookmarkPlus} onclick={() => ontrack(track.secondary[0])}>{track.secondary[1]}</Button>
		{:else}
			<button
				type="button"
				onclick={onstatus}
				class="flex min-h-11 min-w-0 flex-1 items-center gap-2 rounded-xl border border-line-strong bg-surface px-3 text-[0.9375rem] font-medium transition active:scale-[0.98]"
				aria-label="Status: {status}. Change"
			>
				<span class="size-2 shrink-0 rounded-full" style="background: {STATUS_COLOR[status]}"></span>
				<span class="min-w-0 flex-1 truncate text-left">{status}</span>
				<ChevronDown class="size-4 shrink-0 text-faint" />
			</button>
			<button
				type="button"
				onclick={onscore}
				class="flex min-h-11 shrink-0 items-center gap-1.5 rounded-xl border border-line-strong bg-surface px-3 text-[0.9375rem] font-medium tabular-nums transition active:scale-[0.98]"
				aria-label={score ? `Score ${score}. Change` : 'Rate'}
			>
				<Star class="size-4 {score ? 'fill-amber-300 text-amber-300' : 'text-faint'}" />
				{score ?? 'Rate'}
			</button>
			{#if mode === 'episodes' && next}
				<Button
					variant="primary"
					class="order-first w-full"
					icon={next.upcoming ? Clock : Check}
					disabled={next.disabled}
					onclick={onnext}
				>
					{next.label}
				</Button>
			{:else if mode === 'steps' && step}
				<div class="order-first flex w-full items-center justify-between gap-3">
					<span class="min-w-0 truncate pl-1 text-[0.9375rem] font-medium tabular-nums">{step.display}</span>
					<Stepper
						display={step.short}
						label={step.label}
						onstep={onstep}
						canDecrement={step.canDecrement}
						canIncrement={step.canIncrement}
					/>
				</div>
			{:else if mode === 'film' && film}
				{#if !film.done}
					<Button variant="primary" class="shrink-0" icon={Check} onclick={onwatched}>Watched</Button>
				{/if}
			{/if}
		{/if}
	</div>
</div>
