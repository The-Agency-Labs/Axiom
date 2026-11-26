// The chapter each book being read is in (GET /api/v1/reading/chapters),
// from the table of contents the KOReader plugin sends. Shared by every view
// that shows a book's page.

import { get } from './api.ts';

type ChapterAt = { chapter: string; page: number };

const state = $state<{ byItem: Record<string, ChapterAt> }>({ byItem: {} });
let loadedAt = 0;
let inflight: Promise<unknown> | null = null;

/** Fetch (at most every 30 s; callers can call it freely). */
export function loadChapters(force = false) {
	if (inflight || (!force && Date.now() - loadedAt < 30_000)) return;
	loadedAt = Date.now();
	inflight = get<Record<string, ChapterAt>>('/reading/chapters')
		.then((d) => (state.byItem = d ?? {}))
		.catch(() => {})
		.finally(() => (inflight = null));
}

/**
 * "Chapter 12: Page 214" for a book at that page, or null when its chapter
 * isn't known (no table of contents yet, or the page changed since).
 */
export function bookPosition(itemId: number, page: number): string | null {
	const c = state.byItem[String(itemId)];
	return c && c.page === page ? `${c.chapter}: Page ${page}` : null;
}
