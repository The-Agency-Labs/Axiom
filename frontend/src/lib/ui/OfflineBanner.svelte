<script lang="ts">
	import { CloudOff, RefreshCw } from '@lucide/svelte';
	import { net } from '#lib/offline.svelte.ts';

	/**
	 * Status strip: shows when the device is offline (with the number of queued changes) or while queued
	 * changes are syncing. Renders nothing otherwise. Mounted once by the app shell.
	 */
	const visible = $derived(!net.online || net.pending > 0);
</script>

{#if visible}
	<div
		role="status"
		class="flex min-h-9 items-center justify-center gap-2 border-b border-line bg-raised px-4 py-1.5 text-center text-[13px] font-medium text-ink"
	>
		{#if !net.online}
			<CloudOff class="size-4 shrink-0 text-warning" />
			<span>
				Offline - showing saved data{#if net.pending > 0}
					<span class="text-muted"> · {net.pending} change{net.pending === 1 ? '' : 's'} waiting to sync</span>{/if}
			</span>
		{:else}
			<RefreshCw class="size-4 shrink-0 {net.syncing ? 'animate-spin' : ''} text-brand" />
			<span>Syncing {net.pending} change{net.pending === 1 ? '' : 's'}…</span>
		{/if}
	</div>
{/if}
