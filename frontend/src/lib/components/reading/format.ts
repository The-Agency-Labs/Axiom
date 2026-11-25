// Small formatting helpers shared by the reading, series and year pages.

/** "Series 3: Title" from the parts the API sends. */
export function bookTitle(title: string, series?: string | null, number?: number | null) {
	if (!series) return title;
	if (title.toLowerCase().includes(series.toLowerCase())) return title;
	return number ? `${series} ${number}: ${title}` : `${series}: ${title}`;
}

export function isoDate(d: Date) {
	const m = String(d.getMonth() + 1).padStart(2, '0');
	const day = String(d.getDate()).padStart(2, '0');
	return `${d.getFullYear()}-${m}-${day}`;
}

/** Local YYYY-MM-DD, `n` days before today. */
export function isoDaysAgo(n: number) {
	const d = new Date();
	d.setDate(d.getDate() - n);
	return isoDate(d);
}

/** Parse YYYY-MM-DD as a local date (no timezone shift). */
export const parseDay = (iso: string) => new Date(iso + 'T12:00:00');

export function dayHeading(iso: string) {
	if (iso === isoDaysAgo(0)) return 'Today';
	if (iso === isoDaysAgo(1)) return 'Yesterday';
	return parseDay(iso).toLocaleDateString(undefined, { weekday: 'long', month: 'short', day: 'numeric' });
}

/** Title-page link for a stored book: tracked entry, else the metadata page. */
export function bookHref(b: { source: string; media_id: string; entry?: { id: number } | null }) {
	return b.entry?.id ? `/item/book/${b.entry.id}` : `/media/book/${b.source}/${b.media_id}`;
}
