<script lang="ts" module>
	export type SheetName = 'status' | 'score' | 'dates' | 'delete' | null;
</script>

<script lang="ts">
	import { Check, Star } from '@lucide/svelte';
	import { MEDIA, STATUS_COLOR, STATUSES } from '#lib/media.ts';
	import type { MediaType, Status } from '#lib/types.ts';
	import { Button, Sheet, Stepper } from '#lib/ui/index.ts';

	/** The title page's bottom sheets: status, score, dates and notes, delete confirmation. */
	let {
		which = $bindable(null),
		status,
		score,
		start,
		end,
		notes,
		kind,
		title,
		onstatus,
		onscore,
		onsave,
		ondelete
	}: {
		which: SheetName;
		status: Status;
		score: number | null;
		start: string | null;
		end: string | null;
		notes: string;
		kind: MediaType;
		title: string;
		onstatus: (s: Status) => void;
		onscore: (v: number | null) => void;
		onsave: (v: { start_date: string; end_date: string; notes: string }) => void;
		ondelete: () => void;
	} = $props();

	const day = (iso: string | null) => (iso ? iso.slice(0, 10) : '');

	const set = (name: Exclude<SheetName, null>) => (v: boolean) => {
		if (!v && which === name) which = null;
	};

	let draftScore = $state(0);
	let draftStart = $state('');
	let draftEnd = $state('');
	let draftNotes = $state('');
	// Fresh drafts every time a sheet opens.
	$effect(() => {
		if (which === 'score') draftScore = score ?? 0;
		if (which === 'dates') {
			draftStart = day(start);
			draftEnd = day(end);
			draftNotes = notes;
		}
	});

</script>

<Sheet open={which === 'status'} title="Status" onclose={() => set('status')(false)}>
	<ul class="-mx-2 pb-2" role="radiogroup" aria-label="Status">
		{#each STATUSES as s (s)}
			<li>
				<button
					type="button"
					role="radio"
					aria-checked={status === s}
					class="flex min-h-12 w-full items-center gap-3 rounded-xl px-3 text-left text-[0.9375rem] hover:bg-ink/5 {status === s ? 'font-semibold' : ''}"
					onclick={() => {
						which = null;
						if (s !== status) onstatus(s);
					}}
				>
					<span class="size-2.5 rounded-full" style="background: {STATUS_COLOR[s]}"></span>
					<span class="flex-1">{s}</span>
					{#if status === s}<Check class="size-5 text-brand" />{/if}
				</button>
			</li>
		{/each}
	</ul>
</Sheet>

<Sheet open={which === 'score'} title="Your score" onclose={() => set('score')(false)}>
	<div class="flex flex-col items-center gap-4 pb-2">
		<p class="flex items-center gap-2 text-5xl font-semibold tabular-nums">
			<Star class="size-9 fill-amber-300 text-amber-300" />{draftScore ? draftScore : '–'}
		</p>
		<Stepper
			display={draftScore ? String(draftScore) : '0'}
			label="score"
			onstep={(d) => (draftScore = Math.min(10, Math.max(0, draftScore + d * 0.5)))}
			canDecrement={draftScore > 0}
			canIncrement={draftScore < 10}
		/>
		<div class="grid w-full grid-cols-5 gap-2">
			{#each [1, 2, 3, 4, 5, 6, 7, 8, 9, 10] as n (n)}
				<button
					type="button"
					class="h-11 rounded-xl border text-[0.9375rem] font-medium tabular-nums transition active:scale-95 {Math.round(draftScore) === n && Number.isInteger(draftScore)
						? 'border-transparent bg-brand text-on-brand'
						: 'border-line bg-surface hover:bg-raised'}"
					onclick={() => (draftScore = n)}>{n}</button
				>
			{/each}
		</div>
	</div>
	{#snippet footer()}
		<div class="flex gap-2">
			<Button
				full
				onclick={() => {
					which = null;
					onscore(null);
				}}>Clear</Button
			>
			<Button
				full
				variant="primary"
				onclick={() => {
					which = null;
					onscore(draftScore || null);
				}}>Save</Button
			>
		</div>
	{/snippet}
</Sheet>

<Sheet open={which === 'dates'} title="Dates and notes" onclose={() => set('dates')(false)}>
	<div class="space-y-4 pb-2">
		<div class="grid grid-cols-2 gap-3">
			<label class="block">
				<span class="mb-1 block text-xs text-muted">Started</span>
				<input type="date" bind:value={draftStart} class="h-11 w-full rounded-xl border border-line bg-raised px-3" />
			</label>
			<label class="block">
				<span class="mb-1 block text-xs text-muted">Finished</span>
				<input type="date" bind:value={draftEnd} class="h-11 w-full rounded-xl border border-line bg-raised px-3" />
			</label>
		</div>
		<label class="block">
			<span class="mb-1 block text-xs text-muted">Notes</span>
			<textarea
				rows="5"
				bind:value={draftNotes}
				placeholder="Thoughts, favourite moments, where you left off"
				class="w-full resize-y rounded-xl border border-line bg-raised px-3 py-2"
			></textarea>
		</label>
	</div>
	{#snippet footer()}
		<Button
			full
			variant="primary"
			onclick={() => {
				which = null;
				onsave({ start_date: draftStart, end_date: draftEnd, notes: draftNotes });
			}}>Save</Button
		>
	{/snippet}
</Sheet>


<Sheet open={which === 'delete'} title="Remove from library?" onclose={() => set('delete')(false)}>
	<p class="pb-4 text-[0.9375rem] text-muted">
		<strong class="text-ink">{title}</strong> will be removed from your library, along with its progress, score and notes.
	</p>
	{#snippet footer()}
		<div class="flex gap-2">
			<Button full onclick={() => (which = null)}>Keep it</Button>
			<Button
				full
				variant="danger"
				onclick={() => {
					which = null;
					ondelete();
				}}>Remove</Button
			>
		</div>
	{/snippet}
</Sheet>
