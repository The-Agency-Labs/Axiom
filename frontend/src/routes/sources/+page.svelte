<script lang="ts">
	import { BookOpen, ChevronLeft, Eye, Gamepad2, RefreshCw, Sparkles, Tv, Upload, Webhook } from '@lucide/svelte';
	import { ApiError, get, post, upload } from '#lib/api.ts';
	import SourceCard, { type SourceStatus } from '#lib/components/SourceCard.svelte';
	import { toast } from '#lib/toast.svelte.ts';
	import { Button, PullToRefresh, Skeleton } from '#lib/ui/index.ts';

	interface Source {
		name: string;
		kinds: string[];
		configured: boolean;
		status: SourceStatus | null;
	}
	interface Change {
		kind: string;
		title: string;
		what: string;
	}
	interface DryRun {
		counts: Record<string, number>;
		warnings: string[];
		changes?: Change[];
	}

	let sources = $state<Source[] | null>(null);
	let error = $state('');
	let busy = $state<string | null>(null);
	let preview = $state<DryRun | null>(null);
	let steamId = $state('');
	let fileInput = $state<HTMLInputElement>();
	let poll: ReturnType<typeof setTimeout> | undefined;

	const by = (name: string) => sources?.find((s) => s.name === name);
	const jellyfin = $derived(by('jellyfin'));
	const hook = $derived(by('jellyfin_webhook'));
	const anilist = $derived(by('anilist'));
	const goodreads = $derived(by('goodreads'));
	const steam = $derived(by('steam'));
	const lastRun = (name: string) => by(name)?.status?.last_run ?? '';

	async function load() {
		try {
			sources = await get<Source[]>('/sources');
			error = '';
		} catch {
			error = 'Could not load sources.';
		}
	}

	/** Importers run as background jobs: refresh the list until the source reports a newer run. */
	function watch(name: string, before: string, label: string) {
		clearTimeout(poll);
		let tries = 0;
		const tick = async () => {
			await load();
			if (lastRun(name) !== before) {
				toast(`${label} finished`, { kind: by(name)?.status?.ok === false ? 'error' : 'success' });
				busy = null;
				return;
			}
			if (++tries < 30) poll = setTimeout(tick, 3000);
			else busy = null;
		};
		poll = setTimeout(tick, 2000);
	}

	async function run(key: string, label: string, source: string, request: () => Promise<unknown>) {
		busy = key;
		const before = lastRun(source);
		try {
			await request();
			toast(`${label} started`, { kind: 'info' });
			watch(source, before, label);
		} catch (e) {
			busy = null;
			toast(e instanceof ApiError ? e.message : `${label} failed`, { kind: 'error' });
		}
	}

	const syncJellyfin = () => run('jellyfin', 'Jellyfin sync', 'jellyfin', () => post('/sources/jellyfin/sync'));
	const syncAnilist = () => run('anilist', 'AniList sync', 'anilist', () => post('/sources/anilist/sync'));
	const syncSteam = () => run('steam', 'Steam sync', 'steam', () => post('/sources/steam/sync'));

	async function previewAnilist() {
		busy = 'anilist-preview';
		try {
			preview = await post<DryRun>('/sources/anilist/sync?dry_run=1');
		} catch (e) {
			toast(e instanceof ApiError ? e.message : 'Preview failed', { kind: 'error' });
		} finally {
			busy = null;
		}
	}

	async function saveSteam(e: SubmitEvent) {
		e.preventDefault();
		await run('steam-id', 'Steam import', 'steam', () => post('/imports/steam', { steam_id: steamId.trim() }));
	}

	async function pickFile(e: Event) {
		const file = (e.currentTarget as HTMLInputElement).files?.[0];
		if (!file) return;
		const form = new FormData();
		form.set('file', file);
		await run('goodreads', 'Goodreads import', 'goodreads', () => upload('/imports/goodreads', form));
		if (fileInput) fileInput.value = '';
	}

	$effect(() => {
		load();
		return () => clearTimeout(poll);
	});
</script>

<svelte:head><title>Sources · Axiom</title></svelte:head>

<a href="/more" class="-ml-2 mb-1 inline-flex min-h-11 items-center gap-1 rounded-lg px-2 text-sm text-muted hover:text-ink lg:hidden">
	<ChevronLeft class="size-4" /> More
</a>
<h1 class="mb-1 text-3xl font-semibold tracking-tight">Sources</h1>
<p class="mb-6 text-sm text-muted">Where your library gets its data from, and how the last import went.</p>

<PullToRefresh onrefresh={load}>
	{#if error}
		<p class="rounded-xl border border-danger/40 bg-danger/10 p-3 text-sm text-danger" role="alert">{error}</p>
	{:else if !sources}
		<div class="grid gap-4 md:grid-cols-2">{#each [0, 1, 2, 3] as i (i)}<Skeleton class="h-40 w-full" />{/each}</div>
	{:else}
		<div class="grid items-start gap-4 md:grid-cols-2">
			{#if jellyfin}
				<SourceCard
					title="Jellyfin"
					subtitle="Watch history for TV, anime and movies"
					icon={Tv}
					accent="var(--color-type-tv)"
					configured={jellyfin.configured}
					status={jellyfin.status}
					hint="Ask the server admin to set JELLYFIN_URL, JELLYFIN_API_KEY and map your Jellyfin user."
				>
					{#snippet actions()}
						<Button icon={RefreshCw} loading={busy === 'jellyfin'} onclick={syncJellyfin}>Sync now</Button>
					{/snippet}
				</SourceCard>
			{/if}

			{#if hook}
				<SourceCard
					title="Jellyfin webhook"
					subtitle="Live updates while you watch"
					icon={Webhook}
					accent="var(--color-type-anime-movie)"
					configured={hook.configured}
					status={hook.status}
				>
					{#snippet extra()}
						{#if !hook.status}
							<p class="mt-3 text-[0.8125rem] text-muted">No event received yet.</p>
						{/if}
						<p class="mt-1 text-[0.8125rem]"><a class="inline-flex min-h-11 items-center text-brand underline-offset-2 hover:underline" href="/settings#integrations">Get your webhook URL in Settings</a></p>
					{/snippet}
				</SourceCard>
			{/if}

			{#if anilist}
				<SourceCard
					title="AniList"
					subtitle="Anime and manga lists"
					icon={Sparkles}
					accent="var(--color-type-anime)"
					configured={anilist.configured}
					status={anilist.status}
					hint="Ask the server admin to map your AniList username (ANILIST_USERS)."
				>
					{#snippet extra()}
						{#if preview}
							<div class="mt-3 rounded-xl border border-line bg-raised p-3 text-[0.8125rem]">
								<p class="font-medium">
									Preview: {Object.entries(preview.counts).filter(([, n]) => n > 0).map(([k, n]) => `${n} ${k.replaceAll('_', ' ')}`).join(', ') || 'no changes'}
								</p>
								{#if preview.changes?.length}
									<ul class="mt-2 max-h-56 space-y-1 overflow-y-auto text-muted">
										{#each preview.changes as c, i (i)}
											<li><span class="text-ink">{c.title}</span> · {c.what}</li>
										{/each}
									</ul>
								{/if}
								{#if preview.warnings?.length}<p class="mt-2 text-warning">{preview.warnings.length} warning(s)</p>{/if}
								<button type="button" class="mt-1 min-h-11 text-sm font-medium text-brand" onclick={() => (preview = null)}>Dismiss</button>
							</div>
						{/if}
					{/snippet}
					{#snippet actions()}
						<Button icon={RefreshCw} loading={busy === 'anilist'} onclick={syncAnilist}>Sync now</Button>
						<Button variant="ghost" icon={Eye} loading={busy === 'anilist-preview'} onclick={previewAnilist}>Preview changes</Button>
					{/snippet}
				</SourceCard>
			{/if}

			{#if goodreads}
				<SourceCard
					title="Goodreads"
					subtitle="Import your library export (CSV)"
					icon={BookOpen}
					accent="var(--color-type-book)"
					configured={goodreads.configured}
					status={goodreads.status}
				>
					{#snippet actions()}
						<input bind:this={fileInput} type="file" accept=".csv,text/csv" class="sr-only" id="gr-file" onchange={pickFile} />
						<Button icon={Upload} loading={busy === 'goodreads'} onclick={() => fileInput?.click()}>Upload CSV</Button>
						<a href="https://www.goodreads.com/review/import" target="_blank" rel="noreferrer" class="inline-flex min-h-11 items-center px-2 text-sm text-brand">Export from Goodreads</a>
					{/snippet}
				</SourceCard>
			{/if}

			{#if steam}
				<SourceCard
					title="Steam"
					subtitle="Your game library and playtime"
					icon={Gamepad2}
					accent="var(--color-type-game)"
					configured={steam.configured}
					status={steam.status}
					hint="Ask the server admin to set STEAM_API_KEY."
				>
					{#snippet extra()}
						{#if steam.configured}
						<form onsubmit={saveSteam} class="mt-3 flex gap-2">
							<input class="input min-w-0 flex-1" bind:value={steamId} required placeholder="SteamID64 or vanity name" aria-label="Steam ID" autocapitalize="none" autocomplete="off" />
							<Button type="submit" variant="primary" loading={busy === 'steam-id'}>Import</Button>
						</form>
						{/if}
					{/snippet}
					{#snippet actions()}
						<Button icon={RefreshCw} loading={busy === 'steam'} onclick={syncSteam}>Sync saved library</Button>
					{/snippet}
				</SourceCard>
			{/if}
		</div>
	{/if}
</PullToRefresh>
