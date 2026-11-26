<script lang="ts">
	import { Check, Copy } from '@lucide/svelte';
	import { toast } from '#lib/toast.svelte.ts';

	/** Read-only value with a Copy button (falls back to selecting the text when the clipboard API is blocked). */
	let { value, label, mono = true }: { value: string; label: string; mono?: boolean } = $props();
	let copied = $state(false);

	async function copy() {
		try {
			await navigator.clipboard.writeText(value);
			copied = true;
			setTimeout(() => (copied = false), 1600);
		} catch {
			toast('Could not copy - select the text and copy it manually', { kind: 'error' });
		}
	}
</script>

<div class="flex items-center gap-2">
	<input
		readonly
		{value}
		aria-label={label}
		onfocus={(e) => e.currentTarget.select()}
		class="input min-w-0 flex-1 truncate {mono ? 'font-mono text-[0.8125rem]' : ''}"
	/>
	<button
		type="button"
		onclick={copy}
		class="inline-flex min-h-11 shrink-0 items-center gap-1.5 rounded-xl border border-line-strong bg-surface px-3.5 text-sm font-medium hover:bg-raised"
		aria-label="Copy {label}"
	>
		{#if copied}<Check class="size-4 text-success" />Copied{:else}<Copy class="size-4" />Copy{/if}
	</button>
</div>
