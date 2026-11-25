<script lang="ts">
	import { Lock, Globe } from '@lucide/svelte';
	import { Button, Sheet, Toggle } from '#lib/ui/index.ts';

	/** Create (no `initial`) or edit a list. `onsave` gets the values; `ondelete` shows a delete button. */
	let {
		open = $bindable(false),
		initial,
		canChangeVisibility = true,
		saving = false,
		onsave,
		ondelete
	}: {
		open: boolean;
		initial?: { name: string; description: string; visibility: 'private' | 'public' };
		canChangeVisibility?: boolean;
		saving?: boolean;
		onsave: (v: { name: string; description: string; visibility: 'private' | 'public' }) => void;
		ondelete?: () => void;
	} = $props();

	let name = $state('');
	let description = $state('');
	let isPublic = $state(false);
	let confirming = $state(false);

	// Reset the form every time the sheet opens.
	$effect(() => {
		if (open) {
			name = initial?.name ?? '';
			description = initial?.description ?? '';
			isPublic = initial?.visibility === 'public';
			confirming = false;
		}
	});
	const valid = $derived(name.trim().length > 0 && name.trim().length <= 255);
	function submit(e: Event) {
		e.preventDefault();
		if (valid) onsave({ name: name.trim(), description, visibility: isPublic ? 'public' : 'private' });
	}
</script>

<Sheet bind:open title={initial ? 'Edit list' : 'New list'}>
	<form onsubmit={submit} class="space-y-4 pt-1" id="list-form">
		<label class="block">
			<span class="mb-1 block text-[13px] font-medium text-muted">Name</span>
			<input class="input" bind:value={name} maxlength="255" required placeholder="Comfort rewatches" autocomplete="off" />
		</label>
		<label class="block">
			<span class="mb-1 block text-[13px] font-medium text-muted">Description (optional)</span>
			<textarea class="input min-h-20 resize-y py-2.5" bind:value={description} maxlength="5000"></textarea>
		</label>
		{#if canChangeVisibility}
			<div class="flex items-center justify-between gap-3 rounded-xl border border-line px-3">
				<span class="flex items-center gap-2 text-[14px]">
					{#if isPublic}<Globe class="size-4 text-brand" />Public: anyone signed in with the link can view{:else}<Lock class="size-4 text-muted" />Private: only you and members{/if}
				</span>
				<Toggle bind:checked={isPublic} label="Public list" />
			</div>
		{/if}
		<Button type="submit" variant="primary" full loading={saving} disabled={!valid}>{initial ? 'Save changes' : 'Create list'}</Button>
		{#if ondelete}
			{#if confirming}
				<Button variant="danger" full onclick={ondelete}>Really delete this list?</Button>
			{:else}
				<Button variant="ghost" full onclick={() => (confirming = true)} class="text-danger">Delete list</Button>
			{/if}
		{/if}
	</form>
</Sheet>
