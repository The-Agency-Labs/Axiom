<script lang="ts">
	import { UserMinus, UserPlus } from '@lucide/svelte';
	import { mutate } from '#lib/api.ts';
	import { Button, Sheet } from '#lib/ui/index.ts';

	let {
		open = $bindable(false),
		listId,
		owner,
		members = $bindable(),
		canEdit
	}: {
		open: boolean;
		listId: number;
		owner: string;
		members: { id: number; username: string }[];
		canEdit: boolean;
	} = $props();

	let username = $state('');
	let busy = $state(false);

	async function add(e: Event) {
		e.preventDefault();
		const name = username.trim();
		if (!name) return;
		busy = true;
		const res = await mutate<{ id: number; username: string }[]>({
			method: 'POST',
			path: `/lists/${listId}/members`,
			body: { username: name },
			label: `Added ${name}`
		});
		busy = false;
		if (res.ok && res.data) {
			// The API answers with the member list.
			members = res.data;
			username = '';
		}
	}

	function remove(m: { id: number; username: string }) {
		return mutate({
			method: 'DELETE',
			path: `/lists/${listId}/members`,
			body: { username: m.username },
			label: `Removed ${m.username}`,
			optimistic: () => {
				const before = members;
				members = members.filter((x) => x.id !== m.id);
				return () => (members = before);
			}
		});
	}
</script>

<Sheet bind:open title="Members">
	<p class="mb-3 text-[0.8125rem] text-muted">Members can add and remove items. Only {owner} can change visibility or delete the list.</p>
	<ul class="divide-y divide-line rounded-xl border border-line">
		<li class="flex min-h-12 items-center justify-between px-3 text-[0.875rem]"><span>{owner}</span><span class="text-xs text-faint">Owner</span></li>
		{#each members as m (m.id)}
			<li class="flex min-h-12 items-center justify-between pl-3 text-[0.875rem]">
				<span>{m.username}</span>
				{#if canEdit}
					<button type="button" class="grid size-11 place-items-center text-muted hover:text-danger" aria-label="Remove {m.username}" onclick={() => remove(m)}>
						<UserMinus class="size-[18px]" />
					</button>
				{/if}
			</li>
		{/each}
	</ul>
	{#if canEdit}
		<form onsubmit={add} class="mt-4 flex gap-2">
			<input class="input min-w-0 flex-1" bind:value={username} placeholder="Username" aria-label="Username to add" autocapitalize="none" autocomplete="off" />
			<Button type="submit" variant="primary" icon={UserPlus} loading={busy} disabled={!username.trim()}>Add</Button>
		</form>
	{/if}
</Sheet>
