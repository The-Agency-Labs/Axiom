<script lang="ts">
	import { goto } from '$app/navigation';
	import { mutate } from '#lib/api.ts';
	import Cover from '#lib/components/Cover.svelte';
	import { isFilmKind, isCountAnime, MEDIA, STATUS_COLOR, STATUSES, titleHref } from '#lib/media.ts';
	import { net } from '#lib/offline.svelte.ts';
	import { toast } from '#lib/toast.svelte.ts';
	import type { Entry, SearchResult, Status } from '#lib/types.ts';
	import { Button, Chip, ChipRow, Sheet } from '#lib/ui/index.ts';

	/** Quick track from a search result: pick a status and, where it makes sense, a starting progress. */
	let {
		open = $bindable(false),
		result,
		ontracked
	}: { open: boolean; result: SearchResult | null; ontracked?: (result: SearchResult, entry: Entry) => void } = $props();

	let status = $state<Status>('Planning');
	let progress = $state('');
	let busy = $state(false);

	const kind = $derived(result?.media_type ?? 'tv');
	const meta = $derived(MEDIA[kind]);
	// Episodes of TMDB shows are marked on the title page; count-based kinds take a number here.
	const askProgress = $derived(
		!!result && !isFilmKind(kind) && (meta.steppable || isCountAnime(kind, result.source)) && kind !== 'game' && kind !== 'boardgame' && kind !== 'season'
	);

	$effect(() => {
		if (open) {
			status = isFilmKind(kind) ? 'Planning' : 'In progress';
			progress = '';
		}
	});

	async function track() {
		const r = result;
		if (!r) return;
		if (!net.online) {
			toast('Searching and tracking need a connection.', { kind: 'error' });
			return;
		}
		busy = true;
		const n = Number(progress);
		const res = await mutate<Entry>({
			path: '/library',
			body: {
				media_type: r.media_type,
				source: r.source,
				media_id: String(r.media_id),
				status,
				...(askProgress && progress !== '' && n >= 0 ? { progress: Math.floor(n) } : {})
			},
			label: `${r.title} tracked`
		});
		busy = false;
		if (res.ok && !res.queued) {
			open = false;
			ontracked?.(r, res.data);
			toast(`${r.title} added`, { kind: 'success', action: { label: 'Open', run: () => goto(titleHref(r, r.media_type, res.data.id)) } });
		}
	}
</script>

<Sheet bind:open title="Track">
	{#if result}
		<div class="flex items-center gap-3 pb-3">
			<Cover src={result.image} title={result.title} type={result.media_type} class="aspect-[2/3] w-12 shrink-0 rounded-lg" />
			<div class="min-w-0">
				<p class="line-clamp-2 font-semibold">{result.title}</p>
				<p class="text-[13px] text-muted">{meta.label}{result.release_year ? ` · ${result.release_year}` : ''}</p>
			</div>
		</div>
		<p class="mb-1 text-xs font-medium text-muted">Status</p>
		<div>
			<ChipRow sticky={false} label="Status" class="!bg-transparent !backdrop-blur-none">
				{#each STATUSES as s (s)}
					<Chip selected={status === s} color={STATUS_COLOR[s]} onclick={() => (status = s)}>{s}</Chip>
				{/each}
			</ChipRow>
		</div>
		{#if askProgress}
			<label class="mt-3 block pb-2">
				<span class="mb-1 block text-xs font-medium text-muted">Already at {meta.unit} (optional)</span>
				<input
					type="number"
					inputmode="numeric"
					min="0"
					bind:value={progress}
					placeholder="0"
					class="h-11 w-full rounded-xl border border-line bg-raised px-3"
				/>
			</label>
		{/if}
	{/if}
	{#snippet footer()}
		<Button full variant="primary" loading={busy} onclick={track}>Track</Button>
	{/snippet}
</Sheet>
