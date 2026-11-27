import {
  BookOpen,
  BookOpenText,
  Clapperboard,
  Film,
  Dices,
  Gamepad2,
  Layers,
  Sparkles,
  Tv,
} from "@lucide/svelte";
import type { Component } from "svelte";
import type { MediaType, Status } from "./types";

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
  tv: {
    label: "TV",
    plural: "TV Shows",
    icon: Tv,
    accent: "var(--color-type-tv)",
    unit: "episode",
    units: "ep",
    steppable: false,
  },
  season: {
    label: "Season",
    plural: "Seasons",
    icon: Tv,
    accent: "var(--color-type-tv)",
    unit: "episode",
    units: "ep",
    steppable: true,
  },
  episode: {
    label: "Episode",
    plural: "Episodes",
    icon: Tv,
    accent: "var(--color-type-tv)",
    unit: "episode",
    units: "ep",
    steppable: false,
  },
  movie: {
    label: "Movie",
    plural: "Movies",
    icon: Clapperboard,
    accent: "var(--color-type-movie)",
    unit: "",
    units: "",
    steppable: false,
  },
  anime: {
    label: "Anime",
    plural: "Anime",
    icon: Sparkles,
    accent: "var(--color-type-anime)",
    unit: "episode",
    units: "ep",
    steppable: true,
  },
  anime_movie: {
    label: "Anime movie",
    plural: "Anime Movies",
    icon: Film,
    accent: "var(--color-type-anime-movie)",
    unit: "",
    units: "",
    steppable: false,
  },
  manga: {
    label: "Manga",
    plural: "Manga",
    icon: BookOpenText,
    accent: "var(--color-type-manga)",
    unit: "chapter",
    units: "ch",
    steppable: true,
  },
  book: {
    label: "Book",
    plural: "Books",
    icon: BookOpen,
    accent: "var(--color-type-book)",
    unit: "page",
    units: "Pages",
    steppable: true,
  },
  comic: {
    label: "Comic",
    plural: "Comics",
    icon: Layers,
    accent: "var(--color-type-comic)",
    unit: "issue",
    units: "iss",
    steppable: true,
  },
  game: {
    label: "Game",
    plural: "Games",
    icon: Gamepad2,
    accent: "var(--color-type-game)",
    unit: "30 min",
    units: "min",
    steppable: true,
  },
  boardgame: {
    label: "Board game",
    plural: "Board games",
    icon: Dices,
    accent: "var(--color-type-boardgame)",
    unit: "play",
    units: "plays",
    steppable: true,
  },
};

/** Sections in the navigation, in order. */
export const LIBRARY_TYPES: MediaType[] = [
  "tv",
  "anime",
  "movie",
  "anime_movie",
  "book",
  "manga",
  "comic",
  "game",
  "boardgame",
];

export const STATUSES: Status[] = [
  "In progress",
  "Planning",
  "Completed",
  "Paused",
  "Dropped",
];

export const STATUS_COLOR: Record<Status, string> = {
  "In progress": "var(--color-status-progress)",
  Planning: "var(--color-status-planning)",
  Completed: "var(--color-status-done)",
  Paused: "var(--color-status-paused)",
  Dropped: "var(--color-status-dropped)",
};

export function isMediaType(value: string): value is MediaType {
  return value in MEDIA;
}

export function progressLabel(
  type: MediaType,
  progress: number,
  total: number | null,
) {
  const meta = MEDIA[type];
  if (type === "movie") return progress ? "Watched" : "";
  if (type === "game") return `${Math.floor(progress / 60)}h ${progress % 60}m`;
  if (type === "manga") return total ? `Chapter ${progress} of ${total}` : `Chapter ${progress}`;
  if (!total) return countLabel(type, progress);
  const units = meta.units ? ` ${meta.units}` : "";
  return `${progress} / ${total}${units}`;
}

/** One progress value on its own: "Chapter 77", "524 Pages", "12 ep". */
export function countLabel(type: MediaType, n: number) {
  if (type === "manga") return `Chapter ${n}`;
  if (type === "book" && n === 1) return "1 Page";
  return MEDIA[type].units ? `${n} ${MEDIA[type].units}` : String(n);
}

export function relativeTime(iso: string | null | undefined) {
  if (!iso) return "";
  const then = new Date(iso).getTime();
  const seconds = Math.round((Date.now() - then) / 1000);
  const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });
  const steps: [number, Intl.RelativeTimeFormatUnit][] = [
    [60, "second"],
    [60, "minute"],
    [24, "hour"],
    [7, "day"],
    [4.35, "week"],
    [12, "month"],
    [Infinity, "year"],
  ];
  let value = seconds;
  for (const [size, unit] of steps) {
    if (Math.abs(value) < size) return rtf.format(-Math.round(value), unit);
    value /= size;
  }
  return "";
}

export function formatDate(
  iso: string | null | undefined,
  opts: Intl.DateTimeFormatOptions = {},
) {
  if (!iso) return "";
  // A bare date ("2027-01-10") is that day wherever you are; parsed as a
  // timestamp it would be midnight UTC, the day before in the Americas.
  return new Date(/^\d{4}-\d{2}-\d{2}$/.test(iso) ? `${iso}T12:00:00` : iso).toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
    ...opts,
  });
}

export function minutesLabel(minutes: number) {
  if (minutes < 60) return `${minutes}m`;
  const h = Math.floor(minutes / 60);
  const m = minutes % 60;
  return m ? `${h}h ${m}m` : `${h}h`;
}

export const SOURCE_NAMES: Record<string, string> = {
  tmdb: "TMDB",
  tvdb: "TVDB",
  mal: "MyAnimeList",
  anilist: "AniList",
  mangaupdates: "MangaUpdates",
  igdb: "IGDB",
  openlibrary: "Open Library",
  hardcover: "Hardcover",
  comicvine: "Comic Vine",
  bgg: "BoardGameGeek",
  manual: "Manual",
};

/** Shows and anime come from TheTVDB (a leftover TMDB show without a TVDB match still works) and have seasons and per-episode watches. */
export const hasEpisodes = (kind: string, source: string) =>
  (kind === "tv" || kind === "anime") &&
  (source === "tvdb" || source === "tmdb");
/** Anime without a TVDB match: just an episode counter (MyAnimeList / AniList). */
export const isCountAnime = (kind: string, source: string) =>
  kind === "anime" && source !== "tvdb" && source !== "tmdb";
export const isFilmKind = (kind: string) =>
  kind === "movie" || kind === "anime_movie";

/**
 * The one title route. `entryId` is only a hint that saves a lookup; the page verifies it.
 * `kind` overrides item.media_type (the Now feed reports "season" for TV seasons; pass the group's kind).
 */
export function titleHref(
  item: { media_type?: string; source: string; media_id: string | number },
  kind?: MediaType | string,
  entryId?: number | null,
) {
  // A season/episode event doesn't say whether its show is tv or anime: "show" resolves it on the title page.
  const raw = kind ?? item.media_type;
  const k = raw === "season" || raw === "episode" ? "show" : raw;
  const hint = entryId ? `?e=${entryId}` : "";
  return `/title/${k}/${item.source}/${encodeURIComponent(String(item.media_id))}${hint}`;
}

/** Local calendar day as YYYY-MM-DD. */
export const todayISO = () => new Date().toLocaleDateString("en-CA");

export const pluralize = (n: number, one: string, many = one + "s") =>
  `${n} ${n === 1 ? one : many}`;

/** Library sort orders; ids match the `sort` default in user settings. */
/** Release status of a title itself (server: catalog.ReleaseStatus). */
export const RELEASE_STATUS: Record<string, { label: string; color: string }> = {
	ongoing: { label: 'Ongoing', color: '#34d399' },
	hiatus: { label: 'On hiatus', color: '#fbbf24' },
	ended: { label: 'Ended', color: '#a1a1aa' },
	cancelled: { label: 'Cancelled', color: '#f87171' },
	upcoming: { label: 'Coming soon', color: '#60a5fa' }
};

/** Kinds with no status: you don't finish a game or a board game, you own and play them. */
export const NO_STATUS = new Set<string>(['game', 'boardgame']);

/** What "year" means in each kind's year view, and the heading for entries without that date. */
export function yearView(kind: string): { label: string; missing: string; added: boolean } {
	switch (kind) {
		case 'movie':
		case 'anime_movie':
			return { label: 'Year watched', missing: 'Not watched yet', added: false };
		case 'book':
		case 'manga':
		case 'comic':
			return { label: 'Year read', missing: 'Not finished', added: false };
		case 'game':
		case 'boardgame':
			return { label: 'Year added', missing: '', added: true };
		default:
			return { label: 'Year finished', missing: 'Not finished', added: false };
	}
}

export const SORTS: [string, string][] = [
  ["year", "Year finished"],
  ["activity", "Recent activity"],
  ["title", "Title"],
  ["score", "Score"],
  ["progress", "Progress"],
  ["start_date", "Started"],
  ["end_date", "Finished"],
];

/** A person's or character's picture, or null: a provider's "no picture" stand-in is none. */
export const picture = (url: string | null | undefined) => (url && !/\/default\.(jpg|png)$/.test(url) ? url : null);
