<script lang="ts">
	import { X } from '@lucide/svelte';
	import type { Snippet } from 'svelte';

	/**
	 * Bottom sheet on phones (drag the handle/header down to close), centred dialog on >= 640px.
	 * Built on native <dialog>.showModal(): focus is trapped inside, Esc closes, the page behind is inert,
	 * and focus returns to the opener. Backdrop tap closes too.
	 *
	 *   <Sheet bind:open title="Edit entry">...</Sheet>
	 */
	let {
		open = $bindable(false),
		title,
		onclose,
		children,
		footer
	}: {
		open: boolean;
		title: string;
		onclose?: () => void;
		children: Snippet;
		footer?: Snippet;
	} = $props();

	let dialog: HTMLDialogElement;
	let panel: HTMLDivElement;
	let dragY = $state(0);
	let dragging = $state(false);
	let startY = 0;
	let startT = 0;
	const titleId = `sheet-${Math.random().toString(36).slice(2, 8)}`;

	$effect(() => {
		if (open && !dialog.open) {
			dialog.showModal();
			document.body.style.overflow = 'hidden';
		} else if (!open && dialog.open) {
			dialog.close();
		}
		return () => {
			document.body.style.overflow = '';
		};
	});

	function closed() {
		// Fires for Esc, backdrop, drag and programmatic close alike.
		document.body.style.overflow = '';
		dragY = 0;
		if (open) open = false;
		onclose?.();
	}

	function backdrop(e: MouseEvent) {
		if (e.target === dialog) open = false;
	}

	function dragStart(e: PointerEvent) {
		dragging = true;
		startY = e.clientY;
		startT = e.timeStamp;
		(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
	}
	function dragMove(e: PointerEvent) {
		if (!dragging) return;
		dragY = Math.max(0, e.clientY - startY);
	}
	function dragEnd(e: PointerEvent) {
		if (!dragging) return;
		dragging = false;
		const velocity = dragY / Math.max(1, e.timeStamp - startT); // px per ms
		if (dragY > panel.offsetHeight * 0.35 || velocity > 0.6) open = false;
		else dragY = 0;
	}
</script>

<dialog
	bind:this={dialog}
	aria-labelledby={titleId}
	onclose={closed}
	onclick={backdrop}
	class="m-0 h-dvh max-h-none w-full max-w-none overflow-hidden bg-transparent p-0 text-ink backdrop:bg-black/55 backdrop:backdrop-blur-[2px] sm:m-auto sm:h-auto sm:max-h-[90dvh] sm:w-[min(32rem,calc(100vw-2rem))] sm:rounded-3xl"
>
	<div
		bind:this={panel}
		class="fixed inset-x-0 bottom-0 flex max-h-[88dvh] flex-col rounded-t-[var(--radius-sheet)] border border-b-0 border-line-strong bg-surface shadow-2xl sm:static sm:max-h-[90dvh] sm:rounded-3xl sm:border-b {dragging
			? ''
			: 'transition-transform duration-200'}"
		style="transform: translateY({dragY}px)"
	>
		<div
			class="shrink-0 cursor-grab touch-none select-none sm:cursor-default"
			onpointerdown={dragStart}
			onpointermove={dragMove}
			onpointerup={dragEnd}
			onpointercancel={dragEnd}
			role="presentation"
		>
			<div class="mx-auto mt-2.5 h-1.5 w-10 rounded-full bg-ink/20 sm:hidden"></div>
			<div class="flex items-center justify-between gap-2 py-1 pr-2 pl-5">
				<h2 id={titleId} class="text-base font-semibold">{title}</h2>
				<button
					type="button"
					class="grid size-11 place-items-center rounded-full text-muted hover:bg-ink/8 hover:text-ink"
					aria-label="Close"
					onpointerdown={(e) => e.stopPropagation()}
					onclick={() => (open = false)}><X class="size-5" /></button
				>
			</div>
		</div>
		<div class="min-h-0 flex-1 overflow-y-auto overscroll-contain px-5 pb-5">
			{@render children()}
		</div>
		{#if footer}
			<div class="shrink-0 border-t border-line px-5 pt-3 pb-[max(env(safe-area-inset-bottom),0.75rem)]">
				{@render footer()}
			</div>
		{:else}
			<div class="pb-[env(safe-area-inset-bottom)]"></div>
		{/if}
	</div>
</dialog>
