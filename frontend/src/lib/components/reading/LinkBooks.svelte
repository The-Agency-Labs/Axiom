<script lang="ts">
	import { CheckCheck, EyeOff, Merge, Search, Wand2 } from '@lucide/svelte';
	import { get, mutate, post, qs } from '#lib/api.ts';
	import Cover from '#lib/components/Cover.svelte';
	import { SOURCE_NAMES, minutesLabel } from '#lib/media.ts';
	import { toast } from '#lib/toast.svelte.ts';
	import { Button, Card, Chip, ChipRow, EmptyState, ListRow, Sheet, Skeleton } from '#lib/ui/index.ts';
	import type { BookSuggestion, KindleDocument } from './types.ts';

	let { onchanged }: { onchanged?: () => void } = $props();

	let docs = $state<KindleDocument[] | null>(null);
	let linkable = $state<KindleDocument[]>([]); // linked documents: merge targets
	let autoBusy = $state(false);

	async function load() {
		try {
			docs = await get<KindleDocument[]>('/reading/documents' + qs({ unlinked: true }));
			// Merge targets: every other document that is not itself ignored or merged away.
			const all = await get<KindleDocument[]>('/reading/documents' + qs({ suggestions: false }));
			linkable = all.filter((d) => !d.ignored && d.merged_into == null);
		} catch {
			docs ??= [];
		}
	}
	$effect(() => {
		load();
	});

	function removeDoc(id: number) {
		const before = docs;
		docs = docs?.filter((d) => d.id !== id) ?? null;
		return () => (docs = before);
	}

	async function link(doc: KindleDocument, s: BookSuggestion) {
		const res = await mutate({
			method: 'POST',
			path: `/reading/documents/${doc.id}/link`,
			body: { source: s.source, media_id: String(s.media_id) },
			label: `Linked ${doc.display_title}`,
			optimistic: () => removeDoc(doc.id),
			onSuccess: () => onchanged?.()
		});
		if (res.ok && !res.queued) toast(`Linked to ${s.title}`, { kind: 'success' });
	}

	async function ignore(doc: KindleDocument) {
		await mutate({
			method: 'POST',
			path: `/reading/documents/${doc.id}/ignore`,
			body: { ignored: true },
			label: `Ignored ${doc.display_title}`,
			optimistic: () => removeDoc(doc.id),
			onSuccess: () => onchanged?.()
		});
	}

	async function autoLink() {
		autoBusy = true;
		try {
			const r = await post<{ linked: number; tried: number }>('/reading/documents/auto-link');
			toast(r.tried ? `Linked ${r.linked} of ${r.tried} books` : 'Nothing new to try', { kind: r.linked ? 'success' : 'info' });
			if (r.linked) onchanged?.();
			await load();
		} catch {
			toast('Auto-link failed', { kind: 'error' });
		} finally {
			autoBusy = false;
		}
	}

	// --- Search other matches sheet ---
	let searchDoc = $state<KindleDocument | null>(null);
	let searchOpen = $state(false);
	let source = $state('hardcover');
	let results = $state<BookSuggestion[] | null>(null);
	let searchError = $state('');
	async function runSearch() {
		if (!searchDoc) return;
		results = null;
		searchError = '';
		try {
			results = await get<BookSuggestion[]>(`/reading/documents/${searchDoc.id}/suggestions` + qs({ source }));
		} catch {
			results = [];
			searchError = `${SOURCE_NAMES[source] ?? source} is not available right now.`;
		}
	}
	function openSearch(doc: KindleDocument) {
		searchDoc = doc;
		searchOpen = true;
		runSearch();
	}

	// --- Merge sheet ---
	let mergeDoc = $state<KindleDocument | null>(null);
	let mergeOpen = $state(false);
	const mergeTargets = $derived(linkable.filter((d) => d.id !== mergeDoc?.id));
	async function merge(target: KindleDocument) {
		const doc = mergeDoc;
		if (!doc) return;
		mergeOpen = false;
		await mutate({
			method: 'POST',
			path: `/reading/documents/${doc.id}/merge`,
			body: { into: target.id },
			label: `Merged into ${target.display_title}`,
			optimistic: () => removeDoc(doc.id),
			onSuccess: () => onchanged?.()
		});
	}
</script>

<Card
	title="Link your books"
	description={docs && docs.length ? `${docs.length} from your Kindle need a match` : 'Match Kindle titles to books in your library'}
	icon={CheckCheck}
	accent="var(--color-type-book)"
>
	{#snippet action()}
		<Button size="sm" variant="secondary" icon={Wand2} loading={autoBusy} onclick={autoLink}>Try auto-link again</Button>
	{/snippet}
	{#if docs === null}
		<div class="space-y-3" aria-hidden="true"><Skeleton class="h-28 w-full" /><Skeleton class="h-28 w-full" /></div>
	{:else if docs.length === 0}
		<EmptyState icon={CheckCheck} title="Everything is linked" description="New Kindle books that need a match will appear here." class="!py-8" />
	{:else}
		<ul class="space-y-4">
			{#each docs as doc (doc.id)}
				<li class="rounded-2xl border border-line bg-canvas/40 p-3">
					<div class="flex items-start justify-between gap-2">
						<div class="min-w-0">
							<p class="line-clamp-2 text-[15px] leading-snug font-semibold">{doc.display_title}</p>
							<p class="mt-0.5 truncate text-[12px] text-faint">
								{doc.authors || 'Unknown author'} · {doc.pages_read} pages · {minutesLabel(doc.minutes)}
							</p>
						</div>
					</div>
					{#if doc.suggestions?.length}
						<p class="mt-3 mb-1.5 text-[12px] font-medium text-muted">Is it one of these?</p>
						<ul class="-mx-1 space-y-0.5">
							{#each doc.suggestions.slice(0, 3) as s (s.source + s.media_id)}
								<li>
									<button
										type="button"
										class="flex min-h-14 w-full items-center gap-3 rounded-xl px-2 py-1.5 text-left transition hover:bg-ink/6 active:bg-ink/10"
										onclick={() => link(doc, s)}
										aria-label="Link to {s.title}"
									>
										<Cover src={s.image} title={s.title} type="book" class="aspect-[2/3] w-9 shrink-0 !rounded-md" />
										<span class="min-w-0 flex-1">
											<span class="line-clamp-2 text-[14px] leading-snug">{s.title}</span>
											<span class="text-[11px] text-faint">{SOURCE_NAMES[s.source] ?? s.source}</span>
										</span>
										<span class="shrink-0 rounded-full bg-brand px-3 py-1.5 text-[12px] font-semibold text-on-brand">Link</span>
									</button>
								</li>
							{/each}
						</ul>
					{:else}
						<p class="mt-3 text-[13px] text-muted">No close matches found.</p>
					{/if}
					<div class="mt-2 flex flex-wrap gap-2">
						<Button size="sm" variant="secondary" icon={Search} onclick={() => openSearch(doc)}>Other matches</Button>
						<Button size="sm" variant="secondary" icon={Merge} onclick={() => ((mergeDoc = doc), (mergeOpen = true))}>Merge</Button>
						<Button size="sm" variant="ghost" icon={EyeOff} onclick={() => ignore(doc)}>Ignore</Button>
					</div>
				</li>
			{/each}
		</ul>
	{/if}
</Card>

<Sheet bind:open={searchOpen} title="Other matches">
	{#if searchDoc}
		<p class="mb-3 text-[13px] text-muted">{searchDoc.display_title}</p>
		<ChipRow sticky={false} label="Search source">
			{#each ['hardcover', 'openlibrary'] as src (src)}
				<Chip
					selected={source === src}
					onclick={() => {
						source = src;
						runSearch();
					}}>{SOURCE_NAMES[src]}</Chip
				>
			{/each}
		</ChipRow>
		<div class="mt-2">
			{#if results === null}
				<div class="space-y-2" aria-hidden="true"><Skeleton class="h-14 w-full" /><Skeleton class="h-14 w-full" /><Skeleton class="h-14 w-full" /></div>
			{:else if results.length === 0}
				<p class="py-6 text-center text-sm text-muted">{searchError || 'No matches from this source.'}</p>
			{:else}
				<ul class="-mx-2">
					{#each results.slice(0, 20) as s (s.source + s.media_id)}
						<li>
							<button
								type="button"
								class="flex min-h-14 w-full items-center gap-3 rounded-xl px-2 py-1.5 text-left hover:bg-ink/6"
								onclick={() => {
									searchOpen = false;
									if (searchDoc) link(searchDoc, s);
								}}
							>
								<Cover src={s.image} title={s.title} type="book" class="aspect-[2/3] w-9 shrink-0 !rounded-md" />
								<span class="min-w-0 flex-1 line-clamp-2 text-[14px] leading-snug">{s.title}</span>
							</button>
						</li>
					{/each}
				</ul>
			{/if}
		</div>
	{/if}
</Sheet>

<Sheet bind:open={mergeOpen} title="Merge duplicate into…">
	{#if mergeDoc}
		<p class="mb-2 text-[13px] text-muted">
			Reading from <span class="font-medium text-ink">{mergeDoc.display_title}</span> will count towards the book you pick.
		</p>
		{#if mergeTargets.length === 0}
			<p class="py-6 text-center text-sm text-muted">No other books to merge into yet.</p>
		{:else}
			<div class="-mx-5 divide-y divide-line">
				{#each mergeTargets as t (t.id)}
					<ListRow title={t.display_title} subtitle={t.item ? 'Linked' : 'Not linked yet'} onclick={() => merge(t)}>
						{#snippet leading()}
							<Cover src={t.item?.image} title={t.display_title} type="book" class="aspect-[2/3] w-9 shrink-0 !rounded-md" />
						{/snippet}
					</ListRow>
				{/each}
			</div>
		{/if}
	{/if}
</Sheet>
