/// <reference types="@sveltejs/kit" />
/// <reference no-default-lib="true"/>
/// <reference lib="esnext" />
/// <reference lib="webworker" />

/**
 * Offline-friendly shell: the app's own files are cached per build, pages
 * fall back to the cached shell when offline, and API/Django routes are never
 * cached (data must always be live).
 */
import { version } from '$app/env';
import { assets, immutable } from '$app/manifest';

const sw = self as unknown as ServiceWorkerGlobalScope;
const CACHE = `encompass-${version}`;
const ASSETS = [...immutable, ...assets].map(({ path }) => (path.startsWith('/') ? path : `/${path}`));
ASSETS.push('/200.html');
const SERVER_PATHS = /^\/(api|kosync|webhook|admin|accounts|settings|import|export|static|health)(\/|$)/;

sw.addEventListener('install', (event) => {
	event.waitUntil(
		caches
			.open(CACHE)
			.then((cache) => cache.addAll(ASSETS))
			.then(() => sw.skipWaiting())
	);
});

sw.addEventListener('activate', (event) => {
	event.waitUntil(
		caches
			.keys()
			.then((keys) => Promise.all(keys.filter((key) => key !== CACHE).map((key) => caches.delete(key))))
			.then(() => sw.clients.claim())
	);
});

sw.addEventListener('fetch', (event) => {
	const request = event.request;
	if (request.method !== 'GET') return;
	const url = new URL(request.url);
	if (url.origin !== sw.location.origin || SERVER_PATHS.test(url.pathname)) return;

	// Built assets are immutable: cache first.
	if (ASSETS.includes(url.pathname)) {
		event.respondWith(caches.match(url.pathname).then((hit) => hit ?? fetch(request)));
		return;
	}

	// Page navigations: network first, cached shell when offline.
	if (request.mode === 'navigate') {
		event.respondWith(
			fetch(request).catch(async () => (await caches.match('/200.html')) ?? Response.error())
		);
	}
});
