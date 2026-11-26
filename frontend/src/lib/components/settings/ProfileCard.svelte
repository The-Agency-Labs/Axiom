<script lang="ts">
	import { UserRound } from '@lucide/svelte';
	import { auth } from '#lib/auth.svelte.ts';
	import Avatar from '#lib/components/Avatar.svelte';
	import { resizeToDataUrl } from '#lib/image.ts';
	import { saveSettings, settings } from '#lib/settings.svelte.ts';
	import { toast } from '#lib/toast.svelte.ts';
	import { Button, Card } from '#lib/ui/index.ts';

	let name = $state(settings.data?.display_name ?? '');
	let saving = $state(false);
	let file: HTMLInputElement;

	async function saveName(e: SubmitEvent) {
		e.preventDefault();
		saving = true;
		try {
			await saveSettings({ display_name: name });
			toast('Name saved', { kind: 'success' });
		} catch (err) {
			toast(err instanceof Error ? err.message : 'Could not save', { kind: 'error' });
		} finally {
			saving = false;
		}
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
	}

	async function remove() {
		try {
			await saveSettings({ avatar: '' });
		} catch (err) {
			toast(err instanceof Error ? err.message : 'Could not remove the picture', { kind: 'error' });
		}
	}
</script>

<Card title="Profile" description="Shown in the sidebar and menu. Your username ({auth.username}) is used to sign in." icon={UserRound}>
	<div class="mb-4 flex items-center gap-4">
		<Avatar class="size-16 text-2xl" />
		<div class="flex flex-wrap gap-2">
			<Button onclick={() => file.click()}>Change picture</Button>
			{#if settings.data?.avatar}<Button onclick={remove}>Remove</Button>{/if}
		</div>
		<input bind:this={file} type="file" accept="image/*" class="hidden" onchange={pick} />
	</div>
	<form onsubmit={saveName} class="space-y-3">
		<label class="block">
			<span class="mb-1 block text-sm font-medium">Display name</span>
			<input class="input" bind:value={name} maxlength="60" placeholder={auth.username ?? ''} autocomplete="nickname" />
		</label>
		<Button type="submit" variant="primary" loading={saving}>Save name</Button>
	</form>
</Card>
