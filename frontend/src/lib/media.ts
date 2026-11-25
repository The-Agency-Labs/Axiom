import {
	BookOpen,
	BookOpenText,
	Clapperboard,
	Film,
	Dices,
	Gamepad2,
	Layers,
	Sparkles,
	Tv
} from '@lucide/svelte';
import type { Component } from 'svelte';
import type { MediaType, Status } from './types';

export interface MediaMeta {
	label: string;
	plural: string;
	icon: Component<any>;
	accent: string;
	unit: string;
	units: string;
	/** Can progress move one step at a time from the UI? */
	steppable: boolean;
}

export const MEDIA: Record<MediaType, MediaMeta> = {
	tv: { label: 'TV', plural: 'TV Shows', icon: Tv, accent: 'var(--color-type-tv)', unit: 'episode', units: 'ep', steppable: false },
	season: { label: 'Season', plural: 'Seasons', icon: Tv, accent: 'var(--color-type-tv)', unit: 'episode', units: 'ep', steppable: true },
	episode: { label: 'Episode', plural: 'Episodes', icon: Tv, accent: 'var(--color-type-tv)', unit: 'episode', units: 'ep', steppable: false },
	movie: { label: 'Movie', plural: 'Movies', icon: Clapperboard, accent: 'var(--color-type-movie)', unit: '', units: '', steppable: false },
	anime: { label: 'Anime', plural: 'Anime', icon: Sparkles, accent: 'var(--color-type-anime)', unit: 'episode', units: 'ep', steppable: true },
	anime_movie: { label: 'Anime movie', plural: 'Anime Movies', icon: Film, accent: 'var(--color-type-anime-movie)', unit: '', units: '', steppable: false },
	manga: { label: 'Manga', plural: 'Manga', icon: BookOpenText, accent: 'var(--color-type-manga)', unit: 'chapter', units: 'ch', steppable: true },
	book: { label: 'Book', plural: 'Books', icon: BookOpen, accent: 'var(--color-type-book)', unit: 'page', units: 'p', steppable: true },
	comic: { label: 'Comic', plural: 'Comics', icon: Layers, accent: 'var(--color-type-comic)', unit: 'issue', units: 'iss', steppable: true },
	game: { label: 'Game', plural: 'Games', icon: Gamepad2, accent: 'var(--color-type-game)', unit: '30 min', units: 'min', steppable: true },
	boardgame: { label: 'Board game', plural: 'Board games', icon: Dices, accent: 'var(--color-type-boardgame)', unit: 'play', units: 'plays', steppable: true }
};

/** Sections in the navigation, in order. */
export const LIBRARY_TYPES: MediaType[] = ['tv', 'anime', 'movie', 'anime_movie', 'book', 'manga', 'comic', 'game', 'boardgame'];

export const STATUSES: Status[] = ['In progress', 'Planning', 'Completed', 'Paused', 'Dropped'];

export const STATUS_COLOR: Record<Status, string> = {
	'In progress': 'var(--color-status-progress)',
	Planning: 'var(--color-status-planning)',
	Completed: 'var(--color-status-done)',
	Paused: 'var(--color-status-paused)',
	Dropped: 'var(--color-status-dropped)'
};

export function isMediaType(value: string): value is MediaType {
	return value in MEDIA;
}

export function progressLabel(type: MediaType, progress: number, total: number | null) {
	const meta = MEDIA[type];
	if (type === 'movie') return progress ? 'Watched' : '';
	if (type === 'game') return `${Math.floor(progress / 60)}h ${progress % 60}m`;
	const units = meta.units ? ` ${meta.units}` : '';
	return total ? `${progress} / ${total}${units}` : `${progress}${units}`;
}

export function relativeTime(iso: string | null | undefined) {
	if (!iso) return '';
	const then = new Date(iso).getTime();
	const seconds = Math.round((Date.now() - then) / 1000);
	const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' });
	const steps: [number, Intl.RelativeTimeFormatUnit][] = [
		[60, 'second'],
		[60, 'minute'],
		[24, 'hour'],
		[7, 'day'],
		[4.35, 'week'],
		[12, 'month'],
		[Infinity, 'year']
	];
	let value = seconds;
	for (const [size, unit] of steps) {
		if (Math.abs(value) < size) return rtf.format(-Math.round(value), unit);
		value /= size;
	}
	return '';
}

export function formatDate(iso: string | null | undefined, opts: Intl.DateTimeFormatOptions = {}) {
	if (!iso) return '';
	return new Date(iso).toLocaleDateString(undefined, {
		month: 'short',
		day: 'numeric',
		...opts
	});
}

export function minutesLabel(minutes: number) {
	if (minutes < 60) return `${minutes}m`;
	const h = Math.floor(minutes / 60);
	const m = minutes % 60;
	return m ? `${h}h ${m}m` : `${h}h`;
}

export const SOURCE_NAMES: Record<string, string> = {
	tmdb: 'TMDB',
	tvdb: 'TVDB',
	mal: 'MyAnimeList',
	anilist: 'AniList',
	mangaupdates: 'MangaUpdates',
	igdb: 'IGDB',
	openlibrary: 'Open Library',
	hardcover: 'Hardcover',
	comicvine: 'Comic Vine',
	bgg: 'BoardGameGeek',
	manual: 'Manual'
};

/** Shows and anime tracked through TMDB have seasons and per-episode watches. */
export const hasEpisodes = (kind: string, source: string) => (kind === 'tv' || kind === 'anime') && source === 'tmdb';
/** Anime without a TMDB match: just an episode counter (MyAnimeList / AniList). */
export const isCountAnime = (kind: string, source: string) => kind === 'anime' && source !== 'tmdb';
export const isFilmKind = (kind: string) => kind === 'movie' || kind === 'anime_movie';

/**
 * The one title route. `entryId` is only a hint that saves a lookup; the page verifies it.
 * `kind` overrides item.media_type (the Now feed reports "season" for TV seasons; pass the group's kind).
 */
export function titleHref(
	item: { media_type?: string; source: string; media_id: string | number },
	kind?: MediaType | string,
	entryId?: number | null
) {
	// A season/episode event doesn't say whether its show is tv or anime: "show" resolves it on the title page.
	const raw = kind ?? item.media_type;
	const k = raw === 'season' || raw === 'episode' ? 'show' : raw;
	const hint = entryId ? `?e=${entryId}` : '';
	return `/title/${k}/${item.source}/${encodeURIComponent(String(item.media_id))}${hint}`;
}

/** Local calendar day as YYYY-MM-DD. */
export const todayISO = () => new Date().toLocaleDateString('en-CA');

export const pluralize = (n: number, one: string, many = one + 's') => `${n} ${n === 1 ? one : many}`;

/** Library sort orders; ids match the `sort` default in user settings. */
export const SORTS: [string, string][] = [
	['activity', 'Recent activity'],
	['title', 'Title'],
	['score', 'Score'],
	['progress', 'Progress'],
	['start_date', 'Started'],
	['end_date', 'Finished']
];
