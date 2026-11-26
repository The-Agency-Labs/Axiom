export type MediaType =
	| 'tv'
	| 'season'
	| 'episode'
	| 'movie'
	| 'anime'
	| 'anime_movie'
	| 'manga'
	| 'game'
	| 'book'
	| 'comic'
	| 'boardgame';

export type Status = 'In progress' | 'Planning' | 'Completed' | 'Paused' | 'Dropped';

export interface Item {
	id: number;
	media_id: string;
	source: string;
	media_type: MediaType;
	title: string;
	image: string;
	season_number: number | null;
	episode_number: number | null;
	/** Release status of the title itself: ongoing, hiatus, ended, cancelled, upcoming. */
	release_status?: string;
}

/** A completed season, for the year view of shows (GET /library/seasons). */
export interface SeasonFinished {
	entry_id: number;
	season_number: number;
	season_title: string;
	episodes: number;
	score: number | null;
	image: string;
	start_date: string | null;
	end_date: string | null;
	item: Pick<Item, 'id' | 'media_id' | 'source' | 'media_type' | 'title' | 'image'>;
}

export interface Entry {
	id: number;
	media_type: MediaType;
	item: Item;
	status: Status;
	score: number | null;
	progress: number;
	progressed_at: string | null;
	start_date: string | null;
	end_date: string | null;
	notes: string;
	created_at: string;
	max_progress?: number;
	/** Anime, including anime tracked as TMDB shows and films. */
	is_anime?: boolean;
}

export interface Episode {
	episode_number: number;
	title: string;
	watched_at: string | null;
}

export interface HistoryRow {
	date: string;
	type: string;
	status: string | null;
	progress: number | null;
	score: number | null;
}

export interface EntryDetail extends Entry {
	history: HistoryRow[];
	seasons?: (Entry & { episodes: Episode[] })[];
	episodes?: Episode[];
	metadata: Record<string, any> | null;
}

export interface NowEntry {
	id: number;
	media_type: MediaType;
	item: Item;
	title: string;
	image: string;
	progress: number;
	total: number | null;
	percent: number | null;
	score: number | null;
	progressed_at: string | null;
	genres: string[];
	synopsis: string | null;
	season_number?: number;
	next: { number: number; title: string | null; air_date?: string } | null;
	last_session?: { started_at: string; pages_read: number; minutes: number };
}

export interface NowGroup {
	media_type: MediaType;
	entries: NowEntry[];
}

export interface Finished {
	id: number;
	media_type: MediaType;
	item: Item;
	score: number | null;
	end_date: string;
}

export interface ReadingDay {
	date: string;
	pages: number;
	chapters: number;
	minutes: number;
	sessions: number;
}

export interface ReadingStats {
	start: string;
	end: string;
	read_today: boolean;
	today: ReadingDay | null;
	current_streak: number;
	longest_streak: number;
	totals: {
		pages: number;
		chapters: number;
		minutes: number;
		sessions: number;
		days_read: number;
		days_in_range: number;
	};
	days: ReadingDay[];
}

export interface ReadingSession {
	id: number;
	source: string;
	title: string;
	item: Item | null;
	started_at: string;
	ended_at: string;
	minutes: number;
	pages_read: number;
	chapters_read: number;
	start_page: number | null;
	end_page: number | null;
	device: string;
}

export interface ReadingDocument {
	id: number;
	title: string;
	authors: string;
	percentage: number | null;
	device: string;
	item: Item | null;
	pages_read: number;
	minutes: number;
	sessions: number;
}

export interface SearchResult {
	media_id: string | number;
	source: string;
	media_type: MediaType;
	title: string;
	image: string;
	release_year?: number | string | null;
}

export interface ActivityEntry {
	date: string;
	media_type: MediaType;
	item: Item & { display_title?: string };
	changes: { description: string; field?: string }[];
}

export interface ShowSeason {
	season_number: number;
	title: string;
	is_special: boolean;
	image: string | null;
	air_date: string | null;
	episode_count: number;
	watched: number;
	percent: number | null;
	entry: { id: number; status: Status; score: number | null } | null;
}

export interface Show {
	source: string;
	media_id: string;
	title: string;
	image: string;
	entry: Entry | null;
	status: string | null;
	completion: {
		watched: number;
		total: number;
		percent: number | null;
		seasons_completed: number;
		seasons: number;
		specials_watched: number;
	};
	seasons: ShowSeason[];
}

export interface ShowEpisode {
	episode_number: number;
	title: string;
	overview: string;
	air_date: string | null;
	aired: boolean;
	runtime: number | null;
	image: string | null;
	kind: string;
	score: number | null;
	watched: boolean;
	watch_count: number;
	watched_at: string | null;
}

export interface SeasonDetail {
	source: string;
	media_id: string;
	season_number: number;
	title: string;
	image: string | null;
	synopsis: string | null;
	entry: Entry | null;
	watched: number;
	total: number;
	runtime_minutes: number;
	episodes: ShowEpisode[];
}

export interface YearTop {
	id: number;
	media_type: MediaType;
	item: Item;
	count: number;
}

export interface YearSection {
	units: number;
	minutes?: number;
	titles: number;
	finished: number;
	started: number;
	by_month: number[];
	top: YearTop[];
}

export interface YearSnapshot {
	year: number;
	generated_at: string;
	totals: { finished: number; titles: number; watch_minutes: number };
	sections: Partial<Record<MediaType, YearSection>>;
	reading: {
		pages: number;
		chapters: number;
		minutes: number;
		sessions: number;
		days_read: number;
		days_in_range: number;
		longest_streak: number;
		best_day: ReadingDay | null;
		pages_by_month: number[];
		minutes_by_month: number[];
		days: ReadingDay[];
	} | null;
	finished: Finished[];
	top_rated: Finished[];
	genres: { name: string; count: number }[];
}

export interface AnimeEpisodes {
	source: string;
	media_id: string;
	title: string;
	entry: Entry | null;
	progress: number;
	total: number;
	runtime_minutes: number;
	episodes: ShowEpisode[];
	providers: string[];
	tmdb: { tmdb_id: number; season_number: number; episode_offset: number } | null;
	anilist_id: number | null;
}

export interface Person {
	id: number;
	name: string;
	native_name: string;
	image: string | null;
	source: string;
	url: string;
}

export interface AlsoIn {
	item: Item;
	entry: { media_type: MediaType; id: number; status: Status; score: number | null };
	character: string;
}

export interface CastCredit {
	person: Person;
	also_in: AlsoIn[];
	character?: string;
	job?: string;
	episode_count?: number | null;
	language?: string;
}

export interface TitleCredits {
	supported: boolean;
	cast: CastCredit[];
	crew: CastCredit[];
	characters: { character: string; image: string | null; role: string; voice_actors: CastCredit[] }[];
	languages?: string[];
}

export interface Appearance {
	item: Item;
	entry: { media_type: MediaType; id: number; status: Status; score: number | null } | null;
	characters: string[];
	character_images?: string[];
	role: 'actor' | 'voice' | 'crew';
	language?: string;
}

export interface LibraryPerson {
	person: Person;
	titles: number;
	appearances: Appearance[];
}

/** Provider metadata for a title (GET /metadata/...); every key is optional. */
export interface TitleMetadata {
	title?: string;
	native_title?: string;
	release_status?: string;
	image?: string;
	backdrop?: string;
	synopsis?: string;
	score?: number | null;
	genres?: (string | { name: string })[];
	details?: Record<string, unknown>;
	max_progress?: number | null;
	season_title?: string;
	[key: string]: unknown;
}

export interface LibraryResponse {
	count: number;
	years: number[];
	results: Entry[];
}

export interface ListSummary {
	id: number;
	name: string;
	items_count: number;
	has_item: boolean;
	image?: string | null;
}
