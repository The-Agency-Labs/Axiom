<script lang="ts">
	import { CircleAlert, CircleCheck, Info, X } from '@lucide/svelte';
	import { dismissToast, toasts } from '#lib/toast.svelte.ts';

	/** Renders the toast stack above the tab bar. Mounted once by the app shell; use toast() to show one. */
	const icons = { info: Info, success: CircleCheck, error: CircleAlert };
	const tint = { info: 'text-brand', success: 'text-success', error: 'text-danger' };
</script>

<div
	class="pointer-events-none fixed inset-x-0 z-[70] flex flex-col items-center gap-2 px-4 bottom-[calc(var(--spacing-tabbar)+env(safe-area-inset-bottom)+0.75rem)] lg:bottom-6"
	role="region"
	aria-label="Notifications"
	aria-live="polite"
>
	{#each toasts.items as item (item.id)}
		{@const Icon = icons[item.kind]}
		<div
			role={item.kind === 'error' ? 'alert' : 'status'}
			class="page-in pointer-events-auto flex w-full max-w-md items-center gap-3 rounded-2xl border border-line-strong bg-raised py-2 pr-2 pl-4 shadow-xl shadow-black/30"
		>
			<Icon class="size-5 shrink-0 {tint[item.kind]}" />
			<p class="min-w-0 flex-1 py-1.5 text-sm">{item.message}</p>
			{#if item.action}
				<button
					type="button"
					class="min-h-11 rounded-lg px-3 text-sm font-semibold text-brand"
					onclick={() => {
						item.action?.run();
						dismissToast(item.id);
					}}>{item.action.label}</button
				>
			{/if}
			<button
				type="button"
				class="grid size-11 shrink-0 place-items-center rounded-full text-muted hover:text-ink"
				aria-label="Dismiss"
				onclick={() => dismissToast(item.id)}><X class="size-4" /></button
			>
		</div>
	{/each}
</div>
