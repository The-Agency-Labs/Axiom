<script lang="ts">
	import { Check, ListPlus } from '@lucide/svelte';
	import { get, mutate, qs } from '#lib/api.ts';
	import type { ListSummary } from '#lib/types.ts';
	import { Button, EmptyState, Sheet, Skeleton } from '#lib/ui/index.ts';

	/** "Add to list": every list you can edit, with a check where the title already is. */
	let { open = $bindable(false), itemId }: { open: boolean; itemId: number } = $props();

	let lists = $state<ListSummary[] | null>(null);
	let failed = $state(false);

	$effect(() => {
		if (!open) return;
		failed = false;
		get<ListSummary[]>('/lists' + qs({ item_id: itemId }))
			.then((d) => (lists = d))
			.catch(() => (failed = true));
	});

	function toggle(list: ListSummary) {
		const adding = !list.has_item;
		mutate({
			method: adding ? 'POST' : 'DELETE',
			path: adding ? `/lists/${list.id}/items` : `/lists/${list.id}/items/${itemId}`,
			body: adding ? { item_id: itemId } : undefined,
			label: `${adding ? 'Added to' : 'Removed from'} ${list.name}`,
			optimistic: () => {
				list.has_item = adding;
				list.items_count += adding ? 1 : -1;
				return () => {
					list.has_item = !adding;
					list.items_count += adding ? -1 : 1;
				};
			}
		});
	}
</script>

<Sheet bind:open title="Add to list">
	{#if failed}
		<EmptyState title="Could not load your lists" class="py-8" />
	{:else if !lists}
		<div class="space-y-2 pb-2">
			{#each [0, 1, 2] as i (i)}<Skeleton class="h-12 w-full" />{/each}
		</div>
	{:else if lists.length === 0}
		<EmptyState icon={ListPlus} title="No lists yet" description="Create a list, then add titles to it." class="py-8">
			{#snippet actions()}<Button variant="primary" href="/lists">Go to lists</Button>{/snippet}
		</EmptyState>
	{:else}
		<ul class="-mx-2 pb-2">
			{#each lists as list (list.id)}
				<li>
					<button
						type="button"
						role="checkbox"
						aria-checked={list.has_item}
						class="flex min-h-12 w-full items-center gap-3 rounded-xl px-3 text-left hover:bg-ink/5"
						onclick={() => toggle(list)}
					>
						<span
							class="grid size-6 shrink-0 place-items-center rounded-md border {list.has_item
								? 'border-transparent bg-brand text-on-brand'
								: 'border-line-strong'}"
						>
							{#if list.has_item}<Check class="size-4" />{/if}
						</span>
						<span class="min-w-0 flex-1 truncate text-[0.9375rem] font-medium">{list.name}</span>
						<span class="text-xs text-faint tabular-nums">{list.items_count}</span>
					</button>
				</li>
			{/each}
		</ul>
	{/if}
</Sheet>
