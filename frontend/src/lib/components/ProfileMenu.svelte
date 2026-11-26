<script lang="ts">
	import { Camera, ChevronsUpDown, LogOut, Pencil, Settings, Trash2 } from '@lucide/svelte';
	import { goto } from '$app/navigation';
	import { logout } from '#lib/api.ts';
	import { auth } from '#lib/auth.svelte.ts';
	import { resetSettings, saveSettings, settings, shownName } from '#lib/settings.svelte.ts';
	import { toast } from '#lib/toast.svelte.ts';
	import { Button, Sheet } from '#lib/ui/index.ts';
	import Avatar from './Avatar.svelte';
	import { resizeToDataUrl } from '#lib/image.ts';

	/** Name + picture button that opens a menu: change picture, change name, settings, sign out. */
	let { up = false, class: className = '' }: { up?: boolean; class?: string } = $props();

	let open = $state(false);
	let root: HTMLDivElement;
	let file: HTMLInputElement;
	let editing = $state(false);
	let name = $state('');
	let saving = $state(false);

	const label = $derived(shownName(auth.username));

	function close() {
		open = false;
	}

	function onwindow(e: Event) {
		if (open && root && !root.contains(e.target as Node)) close();
	}

	function onkey(e: KeyboardEvent) {
		if (e.key === 'Escape' && open) close();
	}

	async function pick() {
		const f = file.files?.[0];
		file.value = '';
		if (!f) return;
		try {
			await saveSettings({ avatar: await resizeToDataUrl(f, 256) });
			toast('Picture updated', { kind: 'success' });
		} catch (err) {
			toast(err instanceof Error ? err.message : 'Could not update the picture', { kind: 'error' });
		}
		close();
	}

	async function removePicture() {
		try {
			await saveSettings({ avatar: '' });
		} catch (err) {
			toast(err instanceof Error ? err.message : 'Could not remove the picture', { kind: 'error' });
		}
		close();
	}

	function editName() {
		name = settings.data?.display_name ?? '';
		editing = true;
		close();
	}

	async function saveName(e: SubmitEvent) {
		e.preventDefault();
		saving = true;
		try {
			await saveSettings({ display_name: name });
			editing = false;
		} catch (err) {
			toast(err instanceof Error ? err.message : 'Could not save the name', { kind: 'error' });
		} finally {
			saving = false;
		}
	}

	async function signOut() {
		close();
		await logout();
		auth.username = null;
		resetSettings();
		goto('/login');
	}

	const item =
		'flex min-h-11 w-full items-center gap-3 rounded-lg px-3 text-left text-sm text-ink transition hover:bg-ink/6';
</script>

<svelte:window onclick={onwindow} onkeydown={onkey} />

<div bind:this={root} class="relative {className}">
	<button
		type="button"
		class="flex min-h-11 w-full items-center gap-2.5 rounded-lg px-2 text-left transition hover:bg-ink/5"
		aria-haspopup="menu"
		aria-expanded={open}
		onclick={() => (open = !open)}
	>
		<Avatar />
		<span class="min-w-0 flex-1 truncate text-sm font-medium">{label}</span>
		<ChevronsUpDown class="size-4 shrink-0 text-faint" />
	</button>

	{#if open}
		<div
			role="menu"
			class="absolute z-50 w-60 rounded-xl border border-line bg-surface p-1.5 shadow-xl {up
				? 'bottom-full left-0 mb-2'
				: 'top-full right-0 mt-2'}"
		>
			<div class="px-3 py-2">
				<p class="truncate text-sm font-semibold">{label}</p>
				{#if settings.data?.display_name}<p class="truncate text-xs text-muted">@{auth.username}</p>{/if}
			</div>
			<div class="my-1 border-t border-line"></div>
			<button role="menuitem" class={item} onclick={() => file.click()}><Camera class="size-4" /> Change picture</button>
			{#if settings.data?.avatar}
				<button role="menuitem" class={item} onclick={removePicture}><Trash2 class="size-4" /> Remove picture</button>
			{/if}
			<button role="menuitem" class={item} onclick={editName}><Pencil class="size-4" /> Change name</button>
			<a role="menuitem" href="/settings" class={item} onclick={close}><Settings class="size-4" /> Settings</a>
			<div class="my-1 border-t border-line"></div>
			<button role="menuitem" class="{item} text-danger" onclick={signOut}><LogOut class="size-4" /> Sign out</button>
		</div>
	{/if}
	<input bind:this={file} type="file" accept="image/*" class="hidden" onchange={pick} />
</div>

<Sheet bind:open={editing} title="Change name">
	<form onsubmit={saveName} class="space-y-3">
		<label class="block">
			<span class="mb-1 block text-sm font-medium">Display name</span>
			<input class="input" bind:value={name} maxlength="60" placeholder={auth.username ?? ''} autocomplete="nickname" />
			<span class="mt-1 block text-xs text-muted">Leave empty to use your username{auth.username ? ` (${auth.username})` : ''}.</span>
		</label>
		<Button type="submit" variant="primary" loading={saving}>Save</Button>
	</form>
</Sheet>
