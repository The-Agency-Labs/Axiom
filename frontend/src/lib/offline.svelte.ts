// Connectivity state plus the offline write queue.
//
// Queue design
// - Only safe, replayable taps are queued (see QUEUEABLE): mark episode(s), +/- progress step,
//   status/score/notes change on an existing entry. Everything else needs a connection.
// - Items live in IndexedDB (`encompass` / `queue`), so they survive reloads and app restarts.
// - Replay is strictly in order (FIFO) and single-flight (a Web Lock keeps two tabs from replaying
//   the same queue). A network error stops the replay and keeps the item; an HTTP error from the
//   server means the change conflicts with reality (deleted entry, invalid state): the item is
//   dropped and a toast tells the user.
// - `net.pending` drives the pending indicator; `onSynced()` lets pages reload after a replay.

import { replay } from './api.ts';
import { toast } from './toast.svelte.ts';

export interface QueuedWrite {
	id?: number;
	method: 'POST' | 'PATCH' | 'PUT' | 'DELETE';
	path: string;
	body?: unknown;
	label: string;
	at: number;
}

/** Paths that are safe to defer and replay in order. */
const QUEUEABLE: [string, RegExp][] = [
	['POST', /^\/library\/[a-z_]+\/\d+\/step$/], // progress step
	['POST', /^\/library$/], // status / score / notes / progress on an entry
	['POST', /^\/anime\/[^/]+\/[^/]+\/episodes$/], // anime episodes
	['POST', /^\/shows\/[^/]+\/[^/]+\/seasons\/\d+\/episodes$/], // tv episodes
	['PUT', /^\/books\/\d+\/notes$/], // Triple P + Reflect notes
	['POST', /^\/library\/[a-z_]+\/\d+\/watches$/] // movie (re)watch
];

export const isQueueable = (method: string, path: string) =>
	QUEUEABLE.some(([m, re]) => m === method && re.test(path.split('?')[0]));

export const net = $state({
	online: typeof navigator === 'undefined' ? true : navigator.onLine,
	pending: 0,
	syncing: false,
	items: [] as QueuedWrite[]
});

// --- IndexedDB (minimal promise wrapper) --------------------------------------------------------

let dbPromise: Promise<IDBDatabase> | null = null;

function openDb() {
	dbPromise ??= new Promise<IDBDatabase>((resolve, reject) => {
		const req = indexedDB.open('encompass', 1);
		req.onupgradeneeded = () => req.result.createObjectStore('queue', { keyPath: 'id', autoIncrement: true });
		req.onsuccess = () => resolve(req.result);
		req.onerror = () => reject(req.error);
	});
	return dbPromise;
}

async function tx<T>(mode: IDBTransactionMode, run: (store: IDBObjectStore) => IDBRequest<T>): Promise<T> {
	const db = await openDb();
	return new Promise<T>((resolve, reject) => {
		const request = run(db.transaction('queue', mode).objectStore('queue'));
		request.onsuccess = () => resolve(request.result);
		request.onerror = () => reject(request.error);
	});
}

async function refresh() {
	try {
		net.items = (await tx('readonly', (s) => s.getAll())) as QueuedWrite[];
		net.pending = net.items.length;
	} catch {
		/* IndexedDB unavailable (private mode): the queue is simply empty */
	}
}

export async function enqueue(write: Omit<QueuedWrite, 'at' | 'id'>) {
	await tx('readwrite', (s) => s.add({ ...write, at: Date.now() }));
	await refresh();
}

export async function clearQueue() {
	try {
		await tx('readwrite', (s) => s.clear());
	} catch {
		/* nothing to clear */
	}
	await refresh();
}

// --- Replay -------------------------------------------------------------------------------------

const syncedListeners = new Set<() => void>();
/** Call `fn` after queued writes were replayed (e.g. to reload the page's data). Returns an unsubscribe. */
export function onSynced(fn: () => void) {
	syncedListeners.add(fn);
	return () => syncedListeners.delete(fn);
}

async function drain() {
	await refresh();
	if (!net.online || net.syncing || net.items.length === 0) return;
	net.syncing = true;
	let sent = 0;
	try {
		for (const item of [...net.items]) {
			try {
				await replay(item.method, item.path, item.body);
				sent++;
			} catch (e) {
				if (e instanceof TypeError) break; // still offline: keep the rest, retry later
				// The server refused it: conflict or invalid now. Drop it and say so.
				toast(`Couldn't sync "${item.label}": ${e instanceof Error ? e.message : 'rejected'}`, {
					kind: 'error',
					duration: 7000
				});
			}
			await tx('readwrite', (s) => s.delete(item.id!));
			await refresh();
		}
	} finally {
		net.syncing = false;
	}
	if (sent > 0) {
		toast(sent === 1 ? 'Synced 1 offline change' : `Synced ${sent} offline changes`, { kind: 'success' });
		syncedListeners.forEach((fn) => fn());
	}
}

/** Replay the queue now (single-flight across tabs). */
export function syncQueue() {
	const run = () => drain().catch(() => {});
	if (typeof navigator !== 'undefined' && navigator.locks) {
		return navigator.locks.request('encompass-queue', { ifAvailable: true }, (lock) => (lock ? run() : undefined));
	}
	return run();
}

let started = false;
/** Wire up online/offline events and replay anything left over from last time. */
export function initOffline() {
	if (started || typeof window === 'undefined') return;
	started = true;
	net.online = navigator.onLine;
	window.addEventListener('online', () => {
		net.online = true;
		syncQueue();
	});
	window.addEventListener('offline', () => (net.online = false));
	document.addEventListener('visibilitychange', () => {
		if (document.visibilityState === 'visible') syncQueue();
	});
	syncQueue();
}
