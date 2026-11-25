// Tiny toast store. Render <Toaster /> once (the shell does); call toast() anywhere.

export type ToastKind = 'info' | 'success' | 'error';

export interface ToastItem {
	id: number;
	message: string;
	kind: ToastKind;
	action?: { label: string; run: () => void };
}

export const toasts = $state<{ items: ToastItem[] }>({ items: [] });
let nextId = 1;

export function dismissToast(id: number) {
	toasts.items = toasts.items.filter((t) => t.id !== id);
}

/** Show a toast. Errors stay longer; an `action` (e.g. Undo) keeps it up for 6 s. */
export function toast(
	message: string,
	opts: { kind?: ToastKind; action?: ToastItem['action']; duration?: number } = {}
) {
	const id = nextId++;
	const kind = opts.kind ?? 'info';
	toasts.items = [...toasts.items.slice(-2), { id, message, kind, action: opts.action }];
	const duration = opts.duration ?? (opts.action ? 6000 : kind === 'error' ? 5500 : 3200);
	setTimeout(() => dismissToast(id), duration);
	return id;
}
