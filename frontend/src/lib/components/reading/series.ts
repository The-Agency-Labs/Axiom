// Shared series helpers: tracking a book from a series list.
import { mutate } from '#lib/api.ts';
import type { Status } from '#lib/types.ts';
import type { SeriesBook } from './types.ts';

/**
 * Track a series book (Planning) or start it (In progress) via the offline-safe library save.
 * `book` is mutated optimistically; `onDone` runs after the server (or the offline queue) accepted it.
 */
export function trackBook(
	book: Pick<SeriesBook, 'title' | 'source' | 'media_id' | 'entry'>,
	status: Status,
	apply: (entry: SeriesBook['entry']) => void,
	onDone?: () => void
) {
	const before = book.entry;
	return mutate<{ id: number; status: Status; progress: number; score: number | null }>({
		path: '/library',
		body: { media_type: 'book', source: book.source, media_id: book.media_id, status },
		label: `${status === 'In progress' ? 'Started' : 'Tracking'} ${book.title}`,
		optimistic: () => {
			apply({ id: before?.id ?? 0, status, progress: before?.progress ?? 0, score: before?.score ?? null });
			return () => apply(before);
		},
		onSuccess: (data) => {
			apply({ id: data.id, status: data.status, progress: data.progress, score: data.score });
			onDone?.();
		}
	});
}
