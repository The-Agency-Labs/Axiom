// Thin client for the Axiom JSON API (session cookie + CSRF header).
//
// Reads: `get()`. Writes that must succeed immediately: `post()/patch()/put()/del()`.
// Taps that should keep working offline (episode mark, progress step, status
// change): `mutate()`, which updates the UI optimistically and queues the
// request in IndexedDB when the network is down (see offline.svelte.ts).

import { clearQueue, enqueue, isQueueable, net } from './offline.svelte.ts';
import { toast } from './toast.svelte.ts';

let csrfToken = '';

export class ApiError extends Error {
	constructor(
		public status: number,
		message: string
	) {
		super(message);
	}
}

/** True for fetch() failures caused by the network (not by an HTTP status). */
export const isNetworkError = (e: unknown) => e instanceof TypeError;

type Method = 'GET' | 'POST' | 'PATCH' | 'PUT' | 'DELETE';

async function request<T>(method: Method, path: string, body?: unknown, retried = false): Promise<T> {
	const headers: Record<string, string> = { Accept: 'application/json' };
	const isForm = typeof FormData !== 'undefined' && body instanceof FormData;
	if (body !== undefined && !isForm) headers['Content-Type'] = 'application/json';
	if (method !== 'GET') {
		if (!csrfToken) await session();
		headers['X-CSRFToken'] = csrfToken;
	}
	const response = await fetch(`/api/v1${path}`, {
		method,
		headers,
		credentials: 'same-origin',
		body: body === undefined ? undefined : isForm ? (body as FormData) : JSON.stringify(body)
	});
	if (!response.ok) {
		let detail = response.statusText;
		try {
			detail = (await response.json()).detail ?? detail;
		} catch {
			/* not JSON */
		}
		// A stale CSRF token (rotated cookie): refresh once and retry.
		if (response.status === 403 && method !== 'GET' && !retried && String(detail).includes('CSRF')) {
			csrfToken = '';
			return request<T>(method, path, body, true);
		}
		throw new ApiError(response.status, typeof detail === 'string' ? detail : 'Request failed');
	}
	if (response.status === 204) return undefined as T;
	return response.json() as Promise<T>;
}

// The service worker answers some GETs from cache and refreshes them in the
// background; it announces the refreshed URL so callers can re-read it.
const staleWaiters = new Map<string, Set<() => void>>();
if (typeof navigator !== 'undefined' && 'serviceWorker' in navigator) {
	navigator.serviceWorker.addEventListener('message', (event) => {
		if (event.data?.type !== 'api-updated') return;
		const waiters = staleWaiters.get(event.data.url as string);
		waiters?.forEach((fn) => fn());
		staleWaiters.delete(event.data.url as string);
	});
}

/**
 * GET. `onFresh` (optional) is called with newer data when the service worker
 * served a cached copy first and the background refresh brought something new.
 */
export async function get<T>(path: string, onFresh?: (data: T) => void): Promise<T> {
	const data = await request<T>('GET', path);
	if (onFresh) {
		const url = new URL(`/api/v1${path}`, location.origin).href;
		const set = staleWaiters.get(url) ?? new Set();
		set.add(() => request<T>('GET', path).then(onFresh, () => {}));
		staleWaiters.set(url, set);
		setTimeout(() => staleWaiters.delete(url), 15_000);
	}
	return data;
}
export const post = <T>(path: string, body?: unknown) => request<T>('POST', path, body ?? {});
export const patch = <T>(path: string, body?: unknown) => request<T>('PATCH', path, body ?? {});
export const put = <T>(path: string, body?: unknown) => request<T>('PUT', path, body ?? {});
export const del = <T>(path: string) => request<T>('DELETE', path);
/** Multipart upload; `form` carries the file field. */
export const upload = <T>(path: string, form: FormData) => request<T>('POST', path, form);

export interface MutateOptions<T> {
	method?: Exclude<Method, 'GET'>;
	/** Path below /api/v1, e.g. `/library/book/12/step`. */
	path: string;
	body?: unknown;
	/** Short human label used in toasts and the pending list, e.g. "Episode 4 marked". */
	label: string;
	/** Apply the change to the UI now. Return a function that undoes it. */
	optimistic?: () => void | (() => void);
	/** Runs with the server response when the request really went through. */
	onSuccess?: (data: T) => void;
	/** Show an error toast on failure (default true). */
	toastOnError?: boolean;
}

export type MutateResult<T> =
	| { ok: true; queued: false; data: T }
	| { ok: true; queued: true; data?: undefined }
	| { ok: false; queued: false; error: string };

/**
 * Optimistic write that survives going offline.
 *
 * - Applies `optimistic()` immediately.
 * - Online: sends the request; on a server error the change is rolled back and a toast explains why.
 * - Offline (or the request fails at the network level) and the request is one of the safe, replayable
 *   taps: it is queued in IndexedDB, replayed in order when back online, and `{ queued: true }` is returned.
 *   Queued items that the server later rejects are dropped with a toast.
 * - Offline for anything else: rolled back with a "needs a connection" toast.
 */
export async function mutate<T = unknown>(opts: MutateOptions<T>): Promise<MutateResult<T>> {
	const method = opts.method ?? 'POST';
	const rollback = opts.optimistic?.();
	const undo = () => {
		if (typeof rollback === 'function') rollback();
	};
	const fail = (error: string): MutateResult<T> => {
		undo();
		if (opts.toastOnError !== false) toast(error, { kind: 'error' });
		return { ok: false, queued: false, error };
	};
	const queue = async (): Promise<MutateResult<T>> => {
		if (!isQueueable(method, opts.path)) return fail('You are offline. This change needs a connection.');
		await enqueue({ method, path: opts.path, body: opts.body, label: opts.label });
		toast(`${opts.label} - saved offline, will sync`, { kind: 'info' });
		return { ok: true, queued: true };
	};

	if (!net.online || net.pending > 0) {
		// Behind a non-empty queue: stay ordered, never overtake queued writes.
		if (!net.online || isQueueable(method, opts.path)) return queue();
	}
	try {
		const data = await request<T>(method, opts.path, opts.body);
		opts.onSuccess?.(data);
		return { ok: true, queued: false, data };
	} catch (e) {
		if (isNetworkError(e)) return queue();
		return fail(e instanceof ApiError ? e.message : 'Request failed');
	}
}

/** Replays one queued write (used by the offline queue). */
export const replay = (method: Exclude<Method, 'GET'>, path: string, body: unknown) =>
	request<unknown>(method, path, body);

export interface Session {
	csrf: string;
	authenticated: boolean;
	username: string | null;
}

export async function session(): Promise<Session> {
	const data = await request<Session>('GET', '/auth/csrf');
	csrfToken = data.csrf;
	return data;
}

export async function login(username: string, password: string) {
	await session();
	const data = await request<{ username: string; csrf: string }>('POST', '/auth/login', {
		username,
		password
	});
	csrfToken = data.csrf;
	return data;
}

export async function logout() {
	await post('/auth/logout');
	csrfToken = '';
	try {
		localStorage.removeItem('axiom-user');
		localStorage.removeItem('axiom-settings');
	} catch {
		/* storage unavailable */
	}
	// Drop cached API data and any queued writes so the next user starts clean.
	await clearQueue();
	navigator.serviceWorker?.controller?.postMessage({ type: 'clear-api-cache' });
}

export function qs(params: Record<string, string | number | boolean | null | undefined>) {
	const search = new URLSearchParams();
	for (const [key, value] of Object.entries(params)) {
		if (value !== null && value !== undefined && value !== '') search.set(key, String(value));
	}
	const text = search.toString();
	return text ? `?${text}` : '';
}
