<script lang="ts">
	import { UserRound } from '@lucide/svelte';
	import { get } from '#lib/api.ts';
	import type { TitleCredits } from '#lib/types.ts';
	import { Chip, Skeleton } from '#lib/ui/index.ts';

	/** Cast & crew (or characters with voice actors for anime) as a horizontal strip linking to /people/[id]. */
	let {
		kind,
		source,
		mediaId,
		accent
	}: { kind: string; source: string; mediaId: string; accent: string } = $props();

	let data = $state<TitleCredits | null>(null);
	let language = $state('japanese');
	let all = $state(false);

	$effect(() => {
		void kind;
		void source;
		void mediaId;
		data = null;
		all = false;
		get<TitleCredits>(`/credits/${kind}/${source}/${encodeURIComponent(mediaId)}`)
			.then((d) => {
				data = d;
				if (d.languages?.length && !d.languages.includes(language)) language = d.languages[0];
			})
			.catch(() => (data = { supported: false, cast: [], crew: [], characters: [] }));
	});

	interface Card {
		key: string;
		personId: number;
		name: string;
		image: string | null;
		role: string;
		sub?: string;
	}

	const cards = $derived.by<Card[]>(() => {
		if (!data) return [];
		if (data.characters.length)
			return data.characters
				.map((c) => ({ c, actor: c.voice_actors.find((v) => v.language === language) ?? null }))
				.filter(({ c, actor }) => actor || c.role === 'main')
				.filter(({ actor }) => actor)
				.map(({ c, actor }, i) => ({
					key: `${c.character}${i}`,
					personId: actor!.person.id,
					name: actor!.person.name,
					image: actor!.person.image,
					role: c.character,
					sub: actor!.also_in.length ? `Also in ${actor!.also_in[0].item.title}` : c.role
				}));
		return data.cast.map((c) => ({
			key: String(c.person.id) + (c.character ?? ''),
			personId: c.person.id,
			name: c.person.name,
			image: c.person.image,
			role: c.character ?? '',
			sub: c.also_in.length ? `Also in ${c.also_in[0].item.title}` : c.episode_count ? `${c.episode_count} ep` : undefined
		}));
	});

	const crewLine = $derived.by(() => {
		const labels: Record<string, string> = {
			Director: 'Directed by',
			Screenplay: 'Written by',
			Writer: 'Written by',
			Novel: 'Based on the novel by',
			'Original Music Composer': 'Music by'
		};
		const groups = new Map<string, string[]>();
		for (const c of data?.crew ?? []) {
			const label = labels[c.job ?? ''] ?? c.job ?? '';
			const names = groups.get(label) ?? [];
			if (!names.includes(c.person.name)) groups.set(label, [...names, c.person.name]);
		}
		return [...groups.entries()].slice(0, 4);
	});
</script>

{#if !data}
	<section>
		<Skeleton class="mb-3 h-5 w-32" />
		<div class="flex gap-3 overflow-hidden">
			{#each [0, 1, 2, 3, 4] as i (i)}<Skeleton class="h-28 w-24 shrink-0" />{/each}
		</div>
	</section>
{:else if data.supported && cards.length}
	<section>
		<div class="mb-2 flex items-center justify-between gap-3">
			<h2 class="text-[0.9375rem] font-semibold">{data.characters.length ? 'Characters & voice cast' : 'Cast & crew'}</h2>
			{#if (data.languages?.length ?? 0) > 1}
				<div class="flex gap-1.5">
					{#each data.languages ?? [] as lang (lang)}
						<Chip selected={language === lang} onclick={() => (language = lang)} class="capitalize">{lang}</Chip>
					{/each}
				</div>
			{/if}
		</div>
		{#if crewLine.length}
			<p class="mb-3 text-[0.8125rem] text-muted">
				{#each crewLine as [label, names], i (label)}{#if i}<span class="text-faint"> · </span>{/if}{label}
					<span class="text-ink">{names.slice(0, 3).join(', ')}</span>{/each}
			</p>
		{/if}
		<div class="shelf -mx-4 flex gap-3 overflow-x-auto px-4 pb-2 sm:mx-0 sm:px-0 scroll-px-4 sm:scroll-px-0">
			{#each all ? cards : cards.slice(0, 20) as c (c.key)}
				<a href="/people/{c.personId}" class="group flex w-24 shrink-0 flex-col items-center rounded-xl py-1 text-center">
					{#if c.image}
						<img src={c.image} alt="" loading="lazy" class="size-16 rounded-full object-cover ring-1 ring-line transition group-hover:ring-line-strong" />
					{:else}
						<span class="grid size-16 place-items-center rounded-full bg-raised text-faint ring-1 ring-line"><UserRound class="size-6" /></span>
					{/if}
					<span class="mt-2 line-clamp-1 w-full text-[0.8125rem] font-medium">{c.name}</span>
					<span class="line-clamp-2 w-full text-[0.75rem] leading-snug text-faint">{c.role}</span>
					{#if c.sub}<span class="mt-0.5 line-clamp-1 w-full text-[0.75rem]" style="color: {accent}">{c.sub}</span>{/if}
				</a>
			{/each}
			{#if !all && cards.length > 20}
				<button type="button" class="grid min-h-11 w-24 shrink-0 place-items-center text-[0.8125rem] font-medium text-muted hover:text-ink" onclick={() => (all = true)}>
					+{cards.length - 20} more
				</button>
			{/if}
		</div>
	</section>
{/if}
