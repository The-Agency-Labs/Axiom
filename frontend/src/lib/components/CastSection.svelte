<script lang="ts">
	import { Link2, UserRound } from '@lucide/svelte';
	import { get } from '#lib/api.ts';
	import type { AlsoIn, CastCredit, TitleCredits } from '#lib/types.ts';

	/** Cast (TV/movies) or characters with voice actors (anime), linking to people pages. */
	let {
		mediaType,
		source,
		mediaId,
		accent
	}: { mediaType: string; source: string; mediaId: string; accent: string } = $props();

	let data = $state<TitleCredits | null>(null);
	let language = $state('japanese');
	let showAll = $state(false);

	$effect(() => {
		void mediaType;
		void source;
		void mediaId;
		data = null;
		showAll = false;
		get<TitleCredits>(`/credits/${mediaType}/${source}/${mediaId}`)
			.then((d) => {
				data = d;
				if (d.languages?.length && !d.languages.includes(language)) language = d.languages[0];
			})
			.catch(() => (data = { supported: false, cast: [], crew: [], characters: [] }));
	});

	const characters = $derived(
		(data?.characters ?? [])
			.map((c) => ({ ...c, actor: c.voice_actors.find((v) => v.language === language) ?? null }))
			.filter((c) => c.actor || c.role === 'main')
	);
	const LIMIT = 12;
	const crewLine = $derived.by(() => {
		const groups = new Map<string, string[]>();
		for (const c of data?.crew ?? []) {
			const label =
				{ Director: 'Directed by', Screenplay: 'Written by', Writer: 'Written by', Novel: 'Based on the novel by', 'Original Music Composer': 'Music by' }[
					c.job ?? ''
				] ?? c.job!;
			const names = groups.get(label) ?? [];
			if (!names.includes(c.person.name)) groups.set(label, [...names, c.person.name]);
		}
		return [...groups.entries()];
	});

	function alsoLabel(also: AlsoIn[]) {
		if (!also.length) return '';
		return also.length === 1 ? `Also in ${also[0].item.title}` : `Also in ${also[0].item.title} +${also.length - 1}`;
	}
</script>

{#snippet avatar(person: CastCredit['person'], size: string)}
	{#if person.image}
		<img src={person.image} alt="" loading="lazy" class="{size} shrink-0 rounded-full object-cover" />
	{:else}
		<span class="{size} grid shrink-0 place-items-center rounded-full bg-ink/5 text-faint"><UserRound class="size-1/2" /></span>
	{/if}
{/snippet}

{#if data?.supported && (data.cast.length || data.characters.length)}
	<section>
		<div class="mb-3 flex items-center justify-between gap-3">
			<h2 class="text-[15px] font-semibold">{data.characters.length ? 'Characters & voice cast' : 'Cast'}</h2>
			{#if (data.languages?.length ?? 0) > 1}
				<div class="flex rounded-full border border-line p-0.5 text-xs">
					{#each data.languages ?? [] as lang (lang)}
						<button
							onclick={() => (language = lang)}
							class="rounded-full px-2.5 py-1 capitalize transition {language === lang ? 'bg-ink/10 text-ink' : 'text-muted hover:text-ink'}"
						>
							{lang === 'japanese' ? 'Japanese' : lang}
						</button>
					{/each}
				</div>
			{/if}
		</div>

		{#if crewLine.length}
			<p class="mb-4 text-sm text-muted">
				{#each crewLine as [label, names], i (label)}
					{#if i}<span class="text-faint"> · </span>{/if}{label} <span class="text-ink">{names.slice(0, 3).join(', ')}</span>
				{/each}
			</p>
		{/if}

		{#if data.characters.length}
			<ul class="grid gap-2 sm:grid-cols-2">
				{#each showAll ? characters : characters.slice(0, LIMIT) as c, i (c.character + i)}
					<li class="flex items-center gap-3 rounded-xl border border-line bg-surface p-2.5">
						{#if c.image}
							<img src={c.image} alt="" loading="lazy" class="h-14 w-10 shrink-0 rounded-md object-cover" />
						{:else}
							<span class="h-14 w-10 shrink-0 rounded-md bg-ink/5"></span>
						{/if}
						<div class="min-w-0 flex-1">
							<p class="truncate text-sm font-medium">{c.character}</p>
							<p class="text-[11px] text-faint capitalize">{c.role}</p>
						</div>
						{#if c.actor}
							<a href="/people/{c.actor.person.id}" class="flex min-w-0 items-center gap-2.5 text-right">
								<div class="min-w-0">
									<p class="truncate text-sm">{c.actor.person.name}</p>
									{#if c.actor.also_in.length}
										<p class="truncate text-[11px]" style="color: {accent}" title={c.actor.also_in.map((a) => `${a.item.title} (${a.character})`).join('\n')}>
											{alsoLabel(c.actor.also_in)}
										</p>
									{:else if c.actor.person.native_name}
										<p class="truncate text-[11px] text-faint">{c.actor.person.native_name}</p>
									{/if}
								</div>
								{@render avatar(c.actor.person, 'size-10')}
							</a>
						{/if}
					</li>
				{/each}
			</ul>
			{#if characters.length > LIMIT}
				<button class="mt-3 text-xs text-muted hover:text-ink" onclick={() => (showAll = !showAll)}>
					{showAll ? 'Show fewer' : `Show all ${characters.length} characters`}
				</button>
			{/if}
		{:else}
			<div class="shelf -mx-4 flex gap-3 overflow-x-auto px-4 pb-2 sm:mx-0 sm:px-0">
				{#each data.cast as c (c.person.id)}
					<a href="/people/{c.person.id}" class="group w-28 shrink-0">
						<div class="relative">
							{#if c.person.image}
								<img
									src={c.person.image}
									alt=""
									loading="lazy"
									class="aspect-[2/3] w-full rounded-xl object-cover ring-1 ring-line transition group-hover:ring-line-strong"
								/>
							{:else}
								<span class="grid aspect-[2/3] w-full place-items-center rounded-xl bg-ink/5 text-faint ring-1 ring-line">
									<UserRound class="size-8" />
								</span>
							{/if}
							{#if c.also_in.length}
								<span
									class="absolute top-1.5 right-1.5 flex items-center gap-1 rounded-full px-1.5 py-0.5 text-[10px] font-semibold text-[#09090b]"
									style="background: {accent}"
									title={c.also_in.map((a) => `${a.item.title} (${a.character})`).join('\n')}
								>
									<Link2 class="size-3" />{c.also_in.length}
								</span>
							{/if}
						</div>
						<p class="mt-1.5 line-clamp-1 text-xs font-medium">{c.person.name}</p>
						<p class="line-clamp-2 text-[11px] leading-snug text-faint">{c.character}</p>
						{#if c.also_in.length}
							<p class="mt-0.5 line-clamp-1 text-[10px]" style="color: {accent}">{alsoLabel(c.also_in)}</p>
						{:else if c.episode_count && mediaType === 'tv'}
							<p class="text-[10px] text-faint">{c.episode_count} ep</p>
						{/if}
					</a>
				{/each}
			</div>
		{/if}
	</section>
{/if}
