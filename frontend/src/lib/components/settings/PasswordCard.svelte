<script lang="ts">
	import { KeyRound } from '@lucide/svelte';
	import { post } from '#lib/api.ts';
	import { toast } from '#lib/toast.svelte.ts';
	import { Button, Card } from '#lib/ui/index.ts';

	let current = $state('');
	let next = $state('');
	let again = $state('');
	let saving = $state(false);
	let error = $state('');

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		error = '';
		if (next.length < 8) return (error = 'New password must be at least 8 characters.');
		if (next !== again) return (error = 'The new passwords do not match.');
		saving = true;
		try {
			await post('/settings/password', { current, new: next });
			current = next = again = '';
			toast('Password changed', { kind: 'success' });
		} catch (err) {
			error = err instanceof Error ? err.message : 'Could not change the password';
		} finally {
			saving = false;
		}
	}
</script>

<Card title="Password" icon={KeyRound}>
	<form onsubmit={submit} class="space-y-3">
		<label class="block">
			<span class="mb-1 block text-sm font-medium">Current password</span>
			<input class="input" type="password" autocomplete="current-password" required bind:value={current} />
		</label>
		<label class="block">
			<span class="mb-1 block text-sm font-medium">New password</span>
			<input class="input" type="password" autocomplete="new-password" minlength="8" required bind:value={next} />
		</label>
		<label class="block">
			<span class="mb-1 block text-sm font-medium">Repeat new password</span>
			<input class="input" type="password" autocomplete="new-password" minlength="8" required bind:value={again} />
		</label>
		{#if error}<p class="text-sm text-danger" role="alert">{error}</p>{/if}
		<Button type="submit" variant="primary" loading={saving}>Change password</Button>
	</form>
</Card>
