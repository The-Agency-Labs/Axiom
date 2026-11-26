/// <reference types="@sveltejs/kit" />
/// <reference no-default-lib="true"/>
/// <reference lib="esnext" />
/// <reference lib="webworker" />

/**
 * Caching strategy
 *
 * - App shell (built JS/CSS/static files + 200.html): precached per build; cache-first.
 * - Navigations: network first (4 s timeout), cached 200.html when offline. The SPA then renders
 *   from cached API data.
 * - GET /api/v1/*: stale-while-revalidate (answer from cache instantly, refresh in the background and
 *   tell open pages via postMessage `api-updated` when the body changed). Exceptions:
 *     search endpoints: network first, cache only as an offline fallback;
 *     auth, tokens, users: never cached.
 *   Any successful write (POST/PATCH/PUT/DELETE) under /api/v1 drops the cached API responses so
 *   the next read cannot show data from before the write.
 * - Images (posters, covers; same- and cross-origin): cache-first in a separate cache capped at
 *   IMAGE_LIMIT entries (oldest evicted first).
 * - Other server routes (/kosync, /webhook, /calendar, /health) are never touched.
 */
import { version } from '$app/env';
import { assets, immutable } from '$app/manifest';

const sw = self as unknown as ServiceWorkerGlobalScope;
const SHELL = `axiom-shell-${version}`;
const API = 'axiom-api-v1';
const IMAGES = 'axiom-images-v1';
const IMAGE_LIMIT = 400;
const KEEP = new Set([SHELL, API, IMAGES]);

const ASSETS = [...immutable, ...assets].map(({ path }) => (path.startsWith('/') ? path : `/${path}`));
ASSETS.push('/200.html');
const ASSET_SET = new Set(ASSETS);

const NEVER_CACHE = /^\/api\/v1\/(auth|tokens|users)(\/|$)/;
const NETWORK_FIRST = /^\/api\/v1\/(search|series\/search|people\/search)(\/|$)/;
const OTHER_SERVER = /^\/(kosync|webhook|calendar|health)(\/|$)/;

sw.addEventListener('install', (event) => {
	event.waitUntil(
		caches
			.open(SHELL)
			.then((cache) => cache.addAll(ASSETS))
			.then(() => sw.skipWaiting())
	);
});

sw.addEventListener('activate', (event) => {
	event.waitUntil(
		caches
			.keys()
			.then((keys) => Promise.all(keys.filter((key) => !KEEP.has(key)).map((key) => caches.delete(key))))
			.then(() => sw.clients.claim())
	);
});

sw.addEventListener('message', (event) => {
	if (event.data?.type === 'clear-api-cache') event.waitUntil(caches.delete(API));
});

sw.addEventListener('fetch', (event) => {
	const request = event.request;
	const url = new URL(request.url);

	// Writes: pass through, and invalidate cached reads when one succeeds.
	if (request.method !== 'GET') {
		if (url.origin === sw.location.origin && url.pathname.startsWith('/api/v1/') && !NEVER_CACHE.test(url.pathname)) {
			event.respondWith(
				fetch(request).then((response) => {
					if (response.ok) event.waitUntil(caches.delete(API));
					return response;
				})
			);
		}
		return;
	}

	if (url.origin === sw.location.origin && ASSET_SET.has(url.pathname)) {
		// Built assets are immutable: cache first.
		event.respondWith(caches.match(url.pathname).then((hit) => hit ?? fetch(request)));
		return;
	}

	if (request.destination === 'image') {
		event.respondWith(imageStrategy(request));
		return;
	}

	if (url.origin !== sw.location.origin || OTHER_SERVER.test(url.pathname)) return;

	if (url.pathname.startsWith('/api/v1/')) {
		if (NEVER_CACHE.test(url.pathname)) return;
		event.respondWith(NETWORK_FIRST.test(url.pathname) ? networkFirst(request) : staleWhileRevalidate(event, request));
		return;
	}
	if (url.pathname.startsWith('/api/')) return;

	// Page navigations: network first, cached shell when offline or very slow.
	if (request.mode === 'navigate') {
		event.respondWith(
			fetch(request, { signal: AbortSignal.timeout(4000) }).catch(
				async () => (await caches.match('/200.html')) ?? Response.error()
			)
		);
	}
});

const offlineResponse = () =>
	new Response(JSON.stringify({ detail: 'You are offline and this is not saved yet.' }), {
		status: 503,
		headers: { 'Content-Type': 'application/json' }
	});

async function networkFirst(request: Request) {
	const cache = await caches.open(API);
	try {
		const response = await fetch(request);
		if (response.ok) await cache.put(request, response.clone());
		return response;
	} catch {
		return (await cache.match(request)) ?? offlineResponse();
	}
}

async function staleWhileRevalidate(event: FetchEvent, request: Request) {
	const cache = await caches.open(API);
	const cached = await cache.match(request);

	const refresh = fetch(request).then(async (response) => {
		if (response.ok) {
			const changed = cached ? (await cached.clone().text()) !== (await response.clone().text()) : false;
			await cache.put(request, response.clone());
			if (changed) {
				for (const client of await sw.clients.matchAll({ type: 'window' })) {
					client.postMessage({ type: 'api-updated', url: request.url });
				}
			}
		}
		return response;
	});

	if (cached) {
		event.waitUntil(refresh.catch(() => {}));
		return cached;
	}
	try {
		return await refresh;
	} catch {
		return offlineResponse();
	}
}

async function imageStrategy(request: Request) {
	const cache = await caches.open(IMAGES);
	const hit = await cache.match(request);
	if (hit) return hit;
	try {
		// Prefer a CORS request: opaque cross-origin responses count ~7 MB each against the storage quota.
		const sameOrigin = new URL(request.url).origin === sw.location.origin;
		const response = sameOrigin ? await fetch(request) : await fetch(request.url, { mode: 'cors', credentials: 'omit' }).catch(() => fetch(request));
		if (response.ok) {
			await cache.put(request, response.clone());
			trim(cache);
		}
		return response;
	} catch {
		return Response.error();
	}
}

/** Keep the image cache under IMAGE_LIMIT entries by dropping the oldest. */
async function trim(cache: Cache) {
	const keys = await cache.keys();
	for (const key of keys.slice(0, Math.max(0, keys.length - IMAGE_LIMIT))) await cache.delete(key);
}
