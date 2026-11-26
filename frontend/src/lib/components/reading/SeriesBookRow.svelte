<script lang="ts">
	import Cover from '#lib/components/Cover.svelte';
	import { Button, StatusBadge } from '#lib/ui/index.ts';
	import type { Status } from '#lib/types.ts';
	import { bookHref } from './format.ts';
	import type { SeriesBook } from './types.ts';

	let {
		book,
		highlight = false,
		ontrack
	}: { book: SeriesBook; highlight?: boolean; ontrack: (status: Status) => void } = $props();
</script>

<li class="flex items-center gap-3 px-3 py-2.5 {highlight ? 'bg-brand/8' : ''}">
	<a href={bookHref(book)} class="flex min-w-0 flex-1 items-center gap-3" aria-label="Book {book.position}: {book.title}">
		<Cover src={book.image} title={book.title} type="book" class="aspect-[2/3] w-11 shrink-0 !rounded-md" />
		<span class="min-w-0">
			<span class="block text-[0.75rem] font-semibold tracking-wide text-faint tabular-nums uppercase">Book {book.position}{book.year ? ` · ${book.year}` : ''}</span>
			<span class="line-clamp-2 text-[0.875rem] leading-snug font-medium">{book.title}</span>
			{#if book.entry}<StatusBadge status={book.entry.status} compact class="mt-0.5" />{/if}
		</span>
	</a>
	{#if !book.entry}
		<Button size="sm" variant="secondary" onclick={() => ontrack('Planning')}>Track</Button>
	{:else if book.entry.status === 'Planning' || book.entry.status === 'Paused'}
		<Button size="sm" variant={highlight ? 'primary' : 'secondary'} onclick={() => ontrack('In progress')}>Start</Button>
	{/if}
</li>
