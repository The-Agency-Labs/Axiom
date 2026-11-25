<script lang="ts">
	import { Trash2, Webhook } from '@lucide/svelte';
	import { del, get, post } from '#lib/api.ts';
	import { relativeTime } from '#lib/media.ts';
	import { toast } from '#lib/toast.svelte.ts';
	import { Button, Card, CopyField, IconButton, Skeleton } from '#lib/ui/index.ts';

	interface Token {
		id: number;
		name: string;
		scope: 'read' | 'write';
		prefix: string;
		created_at: string;
		last_used_at: string | null;
		revoked: boolean;
	}

	let tokens = $state<Token[] | null>(null);
	let name = $state('');
	let scope = $state<'read' | 'write'>('read');
	let creating = $state(false);
	let fresh = $state<string | null>(null);

	async function load() {
		try {
			tokens = await get<Token[]>('/tokens');
		} catch {
			tokens = [];
		}
	}

	async function create(e: SubmitEvent) {
		e.preventDefault();
		creating = true;
		try {
			const made = await post<Token & { key: string }>('/tokens', { name: name.trim(), scope });
			fresh = made.key;
			name = '';
			await load();
		} catch (err) {
			toast(err instanceof Error ? err.message : 'Could not create the token', { kind: 'error' });
		} finally {
			creating = false;
		}
	}

	async function revoke(token: Token) {
		if (!confirm(`Revoke "${token.name}"? Apps using it will stop working.`)) return;
		try {
			await del(`/tokens/${token.id}`);
			await load();
			toast('Token revoked', { kind: 'success' });
		} catch (err) {
			toast(err instanceof Error ? err.message : 'Could not revoke', { kind: 'error' });
		}
	}

	$effect(() => {
		load();
	});
</script>

<Card title="API tokens" description="For scripts and apps. A write token can change your library." icon={Webhook}>
	{#if fresh}
		<div class="mb-4 rounded-xl border border-warning/40 bg-warning/10 p-3">
			<p class="mb-2 text-sm font-medium">Copy your new token now. You won't be able to see it again.</p>
			<CopyField value={fresh} label="API token" />
			<button type="button" class="mt-2 min-h-11 text-sm font-medium text-brand" onclick={() => (fresh = null)}>Done</button>
		</div>
	{/if}

	{#if tokens === null}
		<Skeleton class="h-16 w-full" />
	{:else if tokens.length}
		<ul class="mb-4 divide-y divide-line rounded-xl border border-line">
			{#each tokens as token (token.id)}
				<li class="flex items-center gap-2 py-1 pr-1 pl-3 {token.revoked ? 'opacity-50' : ''}">
					<div class="min-w-0 flex-1">
						<p class="truncate text-sm font-medium">
							{token.name}
							<span class="ml-1 rounded-full border border-line px-1.5 py-0.5 align-middle text-[10px] font-semibold tracking-wide text-muted uppercase">{token.revoked ? 'revoked' : token.scope}</span>
						</p>
						<p class="truncate font-mono text-xs text-muted">
							{token.prefix}… · {token.last_used_at ? 'used ' + relativeTime(token.last_used_at) : 'never used'}
						</p>
					</div>
					{#if !token.revoked}
						<IconButton icon={Trash2} label="Revoke {token.name}" onclick={() => revoke(token)} />
					{/if}
				</li>
			{/each}
		</ul>
	{:else}
		<p class="mb-4 text-sm text-muted">No tokens yet.</p>
	{/if}

	<form onsubmit={create} class="space-y-3">
		<label class="block">
			<span class="mb-1 block text-sm font-medium">New token name</span>
			<input class="input" bind:value={name} required maxlength="80" placeholder="e.g. Home Assistant" autocomplete="off" />
		</label>
		<fieldset class="flex gap-2">
			<legend class="sr-only">Scope</legend>
			{#each ['read', 'write'] as s (s)}
				<label
					class="flex min-h-11 flex-1 cursor-pointer items-center justify-center rounded-xl border text-sm font-medium capitalize has-[:checked]:border-brand has-[:checked]:bg-brand/12 has-[:focus-visible]:outline-2 has-[:focus-visible]:outline-offset-2 has-[:focus-visible]:outline-brand {scope === s ? 'border-brand' : 'border-line-strong'}"
				>
					<input type="radio" class="sr-only" name="scope" value={s} bind:group={scope} />
					{s} access
				</label>
			{/each}
		</fieldset>
		<Button type="submit" variant="primary" loading={creating}>Create token</Button>
	</form>
</Card>
