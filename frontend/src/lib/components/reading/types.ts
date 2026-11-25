// API shapes for the reading, series, lists and calendar pages (phase 5C).
import type { Item, MediaType, Status } from '#lib/types.ts';

export interface JournalBook {
	item_id: number | null;
	document_id: number | null;
	title: string;
	series: string | null;
	series_number: number | null;
	image: string;
	minutes: number;
	pages: number;
	chapters: number;
	from_page: number | null;
	to_page: number | null;
}

export interface JournalDay {
	date: string;
	minutes: number;
	pages: number;
	chapters: number;
	books: JournalBook[];
}

export interface BookSuggestion {
	media_id: string | number;
	source: string;
	title: string;
	image?: string;
	media_type?: string;
	[key: string]: unknown;
}

export interface SeriesRef {
	id: number;
	name: string;
	position: number;
}

export interface KindleDocument {
	id: number;
	document_hash: string;
	title: string;
	display_title: string;
	series: string | null;
	series_number: number | null;
	authors: string;
	total_pages: number | null;
	percentage: number | null;
	device: string;
	item: (Item & { series?: SeriesRef | null }) | null;
	ignored: boolean;
	merged_into: number | null;
	pages_read: number;
	minutes: number;
	sessions: number;
	suggestions?: BookSuggestion[];
}

export interface BookNotes {
	preview: string;
	predict: string;
	purpose: 'educate' | 'entertain' | 'persuade' | null;
	purpose_note: string;
	reflect: string;
	updated_at: string;
	prompt_predict: boolean;
}

export interface SeriesBookEntry {
	id: number;
	status: Status;
	progress: number;
	score: number | null;
}

export interface SeriesBook {
	position: number;
	title: string;
	image: string;
	year: number | null;
	media_type: 'book';
	source: string;
	media_id: string;
	item_id: number | null;
	entry: SeriesBookEntry | null;
}

export interface SeriesDetail {
	id: number;
	name: string;
	books_count: number;
	books: SeriesBook[];
}

export interface SeriesSummary {
	id: number;
	name: string;
	image: string;
	tracked: number;
	completed: number;
	total: number;
	next: {
		position: number;
		title: string;
		image: string;
		source: string;
		media_id: string;
		item_id: number | null;
		status: Status | null;
	} | null;
}

export interface SeriesHit {
	id: number | null;
	source: string;
	external_id: string;
	name: string;
	books_count: number;
	tracked: number;
}

export interface CalendarEvent {
	date: string;
	time: string | null;
	all_day: boolean;
	title: string;
	media_type: MediaType;
	item_id: number;
	season: number | null;
	episode: number | null;
	image: string;
}

export interface ListSummary {
	id: number;
	name: string;
	description: string;
	visibility: 'private' | 'public';
	owner: { id: number; username: string };
	created_at: string;
	is_owner: boolean;
	can_edit: boolean;
	can_delete: boolean;
	items_count: number;
	image: string;
	last_item_added: string | null;
}

export interface ListItem {
	item: Item;
	added_at: string;
	position: number;
	entry: { id: number; status: Status; score: number | null; progress: number } | null;
}

export interface ListDetail extends Omit<ListSummary, 'items_count' | 'image' | 'last_item_added'> {
	members: { id: number; username: string }[];
	items: ListItem[];
	items_count: number;
	page: number;
	page_size: number;
	media_types: MediaType[];
}

/** One bar of <BarChart>. */
export interface Bar {
	/** Short x tick label (may be empty). */
	tick: string;
	/** Full label for the tooltip/table, e.g. "Mon, Nov 24". */
	label: string;
	value: number;
}
