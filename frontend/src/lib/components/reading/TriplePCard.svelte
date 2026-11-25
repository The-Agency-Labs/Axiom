<script lang="ts">
	import { BookMarked, ChevronDown, Lightbulb } from '@lucide/svelte';
	import { get, mutate } from '#lib/api.ts';
	import { Card, Skeleton, Tabs } from '#lib/ui/index.ts';
	import type { BookNotes } from './types.ts';

	let { entryId, completed = false }: { entryId: number; completed?: boolean } = $props();

	type Field = 'preview' | 'predict' | 'purpose_note' | 'reflect';
	let notes = $state<BookNotes | null>(null);
	let failed = $state(false);
	let saveState = $state<'idle' | 'saving' | 'saved' | 'error'>('idle');
	let purpose = $state('');

	$effect(() => {
		const id = entryId;
		notes = null;
		failed = false;
		get<BookNotes>(`/books/${id}/notes`)
			.then((n) => {
				if (id !== entryId) return;
				notes = n;
				purpose = n.purpose ?? '';
			})
			.catch(() => (failed = true));
	});

	// Autosave: one debounce timer per field; only the changed field is sent.
	const timers: Partial<Record<Field | 'purpose', ReturnType<typeof setTimeout>>> = {};
	async function save(body: Record<string, unknown>) {
		saveState = 'saving';
		const res = await mutate({
			method: 'PUT',
			path: `/books/${entryId}/notes`,
			body,
			label: 'Notes saved',
			toastOnError: false
		});
		saveState = res.ok ? 'saved' : 'error';
	}
	function edit(field: Field, value: string) {
		if (!notes) return;
		notes[field] = value;
		saveState = 'idle';
		clearTimeout(timers[field]);
		timers[field] = setTimeout(() => save({ [field]: value }), 700);
	}
	function pickPurpose(value: string) {
		if (!notes) return;
		const next = notes.purpose === value ? null : (value as BookNotes['purpose']);
		notes.purpose = next;
		purpose = next ?? '';
		save({ purpose: next });
	}
	// Flush pending edits when leaving the page.
	$effect(() => () => {
		for (const k of Object.keys(timers) as (Field | 'purpose')[]) {
			if (timers[k] !== undefined && notes && k !== 'purpose') {
				clearTimeout(timers[k]);
				void save({ [k]: notes[k as Field] });
			}
		}
	});

	const prompt = $derived(!!notes?.prompt_predict && !notes.predict && !notes.purpose);
	let reflectOpen = $state(false);
	$effect(() => {
		if (completed) reflectOpen = true;
	});
</script>

{#snippet section(label: string, hint: string, open: boolean, body: import('svelte').Snippet)}
	<details {open} class="group border-t border-line first:border-t-0">
		<summary class="flex min-h-12 cursor-pointer list-none items-center justify-between gap-2 py-2 [&::-webkit-details-marker]:hidden">
			<span>
				<span class="text-[14px] font-semibold">{label}</span>
				<span class="ml-1.5 text-[12px] text-faint">{hint}</span>
			</span>
			<ChevronDown class="size-4 text-faint transition group-open:rotate-180" />
		</summary>
		<div class="pb-3">{@render body()}</div>
	</details>
{/snippet}

<Card title="Triple P + Reflect" description="Preview, predict, purpose, then reflect" icon={BookMarked} accent="var(--color-type-book)">
	{#if failed}
		<p class="text-sm text-muted">Notes are not available right now.</p>
	{:else if !notes}
		<Skeleton class="h-32 w-full" />
	{:else}
		{#if prompt}
			<div class="mb-3 flex gap-3 rounded-xl border border-line-strong bg-raised p-3">
				<Lightbulb class="mt-0.5 size-5 shrink-0 text-warning" />
				<div class="min-w-0 flex-1">
					<p class="text-[14px] font-semibold">Before you dive in: what do you expect?</p>
					<p class="mt-0.5 text-[13px] text-muted">A sentence or two now makes the ending more fun to compare.</p>
				</div>
			</div>
		{/if}

		{#snippet previewBody()}
			<textarea
				class="input min-h-28 resize-y py-2.5 leading-relaxed"
				aria-label="Preview"
				placeholder="Synopsis, table of contents, what caught your eye…"
				value={notes!.preview}
				oninput={(e) => edit('preview', e.currentTarget.value)}
			></textarea>
		{/snippet}
		{#snippet predictBody()}
			<textarea
				class="input min-h-24 resize-y py-2.5 leading-relaxed"
				aria-label="Predict"
				placeholder="What do you think will happen, or what will you learn?"
				value={notes!.predict}
				oninput={(e) => edit('predict', e.currentTarget.value)}
			></textarea>
		{/snippet}
		{#snippet purposeBody()}
			<Tabs
				label="Purpose of reading"
				full
				value={purpose}
				onchange={pickPurpose}
				options={[
					{ value: 'educate', label: 'Educate' },
					{ value: 'entertain', label: 'Entertain' },
					{ value: 'persuade', label: 'Persuade' }
				]}
			/>
			<textarea
				class="input mt-2 min-h-20 resize-y py-2.5 leading-relaxed"
				aria-label="Purpose note"
				placeholder="Why this book, and why now?"
				value={notes!.purpose_note}
				oninput={(e) => edit('purpose_note', e.currentTarget.value)}
			></textarea>
		{/snippet}
		{#snippet reflectBody()}
			<textarea
				class="input min-h-28 resize-y py-2.5 leading-relaxed"
				aria-label="Reflect"
				placeholder="How did it land? What was your prediction right or wrong about?"
				value={notes!.reflect}
				oninput={(e) => edit('reflect', e.currentTarget.value)}
			></textarea>
		{/snippet}

		<div class="-mt-1">
			{@render section('Preview', notes.preview ? 'seeded from the synopsis' : '', !notes.preview || prompt, previewBody)}
			{@render section('Predict', '', true, predictBody)}
			{@render section('Purpose', '', true, purposeBody)}
			{@render section('Reflect', completed ? 'you finished it' : 'after you finish', reflectOpen || !!notes.reflect, reflectBody)}
		</div>
		<p class="mt-2 h-4 text-right text-[12px] text-faint" aria-live="polite">
			{#if saveState === 'saving'}Saving…{:else if saveState === 'saved'}Saved{:else if saveState === 'error'}Not saved - will retry on your next edit{/if}
		</p>
	{/if}
</Card>
