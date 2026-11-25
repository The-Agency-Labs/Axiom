<script lang="ts">
	import { ShieldCheck } from '@lucide/svelte';
	import { get, patch, post } from '#lib/api.ts';
	import { toast } from '#lib/toast.svelte.ts';
	import { Button, Card, Skeleton, Toggle } from '#lib/ui/index.ts';

	interface User {
		id: number;
		username: string;
		is_admin: boolean;
		is_active: boolean;
		created_at: string;
		last_login_at: string | null;
	}

	let { me }: { me: string } = $props();

	let users = $state<User[] | null>(null);
	let username = $state('');
	let password = $state('');
	let isAdmin = $state(false);
	let creating = $state(false);

	async function load() {
		try {
			users = await get<User[]>('/users');
		} catch {
			users = [];
		}
	}

	async function create(e: SubmitEvent) {
		e.preventDefault();
		creating = true;
		try {
			await post('/users', { username, password, is_admin: isAdmin });
			username = password = '';
			isAdmin = false;
			toast('User created', { kind: 'success' });
			await load();
		} catch (err) {
			toast(err instanceof Error ? err.message : 'Could not create the user', { kind: 'error' });
		} finally {
			creating = false;
		}
	}

	async function flag(user: User, change: { is_active?: boolean; is_admin?: boolean }) {
		try {
			await patch(`/users/${user.id}`, change);
		} catch (err) {
			toast(err instanceof Error ? err.message : 'Could not update the user', { kind: 'error' });
		}
		await load();
	}

	$effect(() => {
		load();
	});
</script>

<Card title="Users" description="Admin only. Deactivating a user signs them out." icon={ShieldCheck} flush>
	{#if users === null}
		<div class="p-4"><Skeleton class="h-16 w-full" /></div>
	{:else}
		<ul class="divide-y divide-line">
			{#each users as user (user.id)}
				<li class="px-4 py-2">
					<p class="font-medium">
						{user.username}{#if user.username === me}<span class="ml-1 text-xs text-muted">(you)</span>{/if}
					</p>
					<div class="flex flex-wrap items-center gap-x-4">
						<span class="flex items-center text-sm text-muted">
							Active
							<Toggle checked={user.is_active} label="{user.username} active" disabled={user.username === me} onchange={(v) => flag(user, { is_active: v })} />
						</span>
						<span class="flex items-center text-sm text-muted">
							Admin
							<Toggle checked={user.is_admin} label="{user.username} admin" disabled={user.username === me} onchange={(v) => flag(user, { is_admin: v })} />
						</span>
					</div>
				</li>
			{/each}
		</ul>
	{/if}
	<form onsubmit={create} class="space-y-3 border-t border-line p-4">
		<h3 class="text-sm font-medium">Add a user</h3>
		<input class="input" bind:value={username} required placeholder="Username" autocapitalize="none" autocomplete="off" aria-label="Username" />
		<input class="input" type="password" bind:value={password} required minlength="8" placeholder="Password (8+ characters)" autocomplete="new-password" aria-label="Password" />
		<label class="flex min-h-11 items-center gap-3 text-sm">
			<input type="checkbox" bind:checked={isAdmin} class="size-5 accent-[var(--color-brand)]" /> Make admin
		</label>
		<Button type="submit" variant="primary" loading={creating}>Create user</Button>
	</form>
</Card>
