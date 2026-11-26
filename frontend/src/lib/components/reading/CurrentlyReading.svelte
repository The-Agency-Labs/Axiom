<script lang="ts">
	import { mutate, get, qs } from '#lib/api.ts';
	import Cover from '#lib/components/Cover.svelte';
	import { Skeleton, Stepper } from '#lib/ui/index.ts';
	import type { Entry } from '#lib/types.ts';
	import { bookTitle } from './format.ts';

	let { version = 0 }: { version?: number } = $props();

	type BookEntry = Entry & { series?: { id: number; name: string; position: number } | null };
	let books = $state<BookEntry[] | null>(null);

	$effect(() => {
		void version;
		get<{ results: BookEntry[] }>('/library' + qs({ media_type: 'book', status: 'In progress', limit: 20 }), (d) => (books = d.results))
			.then((d) => (books = d.results))
			.catch(() => (books ??= []));
	});

	function step(b: BookEntry, direction: 1 | -1) {
		return mutate({
			path: `/library/book/${b.id}/step`,
			body: { direction: direction === 1 ? 'up' : 'down' },
			label: `${b.item.title} ${direction === 1 ? '+1' : '-1'} page`,
			optimistic: () => {
				const before = b.progress;
				b.progress = Math.max(0, before + direction);
				return () => (b.progress = before);
			}
		});
	}
</script>

{#if books === null}
	<Skeleton class="h-24 w-full" />
{:else if books.length}
	<section aria-label="Currently reading">
		<h2 class="mb-2 px-1 text-[0.9375rem] font-semibold">Currently reading</h2>
		<ul class="space-y-2">
			{#each books as b (b.id)}
				<li class="flex items-center gap-3 rounded-[var(--radius-card)] border border-line bg-surface p-3">
					<a href="/item/book/{b.id}" class="flex min-w-0 flex-1 items-center gap-3">
						<Cover src={b.item.image} title={b.item.title} type="book" class="aspect-[2/3] w-12 shrink-0" />
						<span class="min-w-0">
							<span class="line-clamp-2 text-[0.875rem] leading-snug font-medium"
								>{bookTitle(b.item.title, b.series?.name, b.series?.position)}</span
							>
							<span class="text-[0.75rem] text-faint tabular-nums">Page {b.progress}</span>
						</span>
					</a>
					<Stepper display="{b.progress} p" label="pages of {b.item.title}" onstep={(d) => step(b, d)} canDecrement={b.progress > 0} />
				</li>
			{/each}
		</ul>
	</section>
{/if}
