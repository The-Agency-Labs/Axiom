# Axiom server rewrite (Django → Go)

Status: phase 0 (design). Owner decisions are final unless marked open.

## Goal

Replace the Django/Celery/Valkey/nginx/gunicorn/supervisord stack with one Go
binary and SQLite, keeping everything the owner uses working: the SvelteKit
SPA, `/api/v1`, KOSync, the KOReader plugin, the Jellyfin webhook URL, and all
data. Target: ~15–25 MB RSS for the app (≈70–100 MB in the CT including
Docker), down from ~400 MB.

## Decisions

| Topic | Decision |
|---|---|
| Language | Go (stdlib `net/http`, no framework) |
| Database | SQLite via `modernc.org/sqlite` (pure Go, static binary). WAL mode. |
| Queries / migrations | `sqlc` (typed queries from SQL) + `pressly/goose` (embedded migrations) |
| Deploy | Docker Compose pulling `ghcr.io/the-agency-labs/encompass` (static binary on `scratch`/distroless), built by GitHub Actions. Never build on the CT. Dev happens on the owner's PC; clean up dev artifacts afterwards. |
| Users | Multi-user schema from day one; one user now. Admin creates accounts; invite links later. |
| Media types | `tv`, `anime`, `movie`, `anime_movie`, `manga`, `comic`, `book`, `game`, `boardgame`; each can be toggled per user (owner has comics off). Anime and Anime Movie are their own entry types. |
| Show source (owner, 2026-11-25) | **All shows, western TV and anime, come from TheTVDB** (`items.source = 'tvdb'`, `external_id` = TVDB series id): seasons and episodes in TVDB's aired order, poster, synopsis, genres, status, network and cast. **TMDB is only for movies and anime movies.** TMDB lumps shows into one long season (Re:ZERO, JUJUTSU KAISEN, Bleach with 366 episodes in one); TVDB has the real seasons and Jellyfin numbers by TVDB. This replaces the earlier "anime is TMDB-structured" rule. See "Shows are TVDB". |
| Anime classification | Automatic on add via the Fribb mapping (`thetvdb_id` → anime show, TMDB movie id → anime film); user can override per item. |
| Importers | AniList (+30 min poll), Jellyfin (history + webhook), Goodreads CSV, Steam. Others dropped. |
| Webhooks | Jellyfin only (Plex/Emby dropped). |
| Book metadata | Hardcover first, OpenLibrary fallback. Goodreads has no API (closed 2020): CSV import only. |
| Books: Triple P + Reflect | Per read-through: Preview (seeded from synopsis + TOC when available, editable), Predict (free text), Purpose (educate / entertain / persuade + note), Reflect (after finishing). App only, not on the Kindle. |
| Notifications | None. Import/sync failures show as status on the Sources page. |
| Kept from upstream | Calendar (iCal feed), custom lists. |
| Dropped | Release notifications, daily digest, Plex/Emby, Trakt/Simkl/MAL/Kitsu/IMDb/HLTB/Yamtrack-CSV importers, server-rendered pages, Apprise, demo mode. |
| License | AGPL-3.0 (derived from Yamtrack). |

## Behaviour that must carry over (owner rules)

- Jellyfin: an episode/movie counts as watched at `JELLYFIN_WATCHED_PERCENT` (default 75) of runtime on PlaybackStop.
- Never undo: rewatching, rewinding, or re-scrubbing never lowers progress, un-watches an episode, or reopens a completed show. A completed show only reopens for a season never watched before.
- Imports are additive only: they add watches and move statuses forward (Planning → In progress → Completed), never remove or lower anything. Removing media from Jellyfin changes nothing.
- A show first seen at season N does not create an empty season 1. Exception (owner, 2026-11-25): when **Jellyfin** (webhook or history import) is the first to see a show at season N > 1, seasons 1..N-1 are assumed watched (owner deletes media from Jellyfin to free space): every aired episode gets a watch with source `assumed` dated by air date (never after the first real play), those seasons become Completed; specials untouched; never applied to a show already tracked, to manual adds, or to AniList. Assumed watches count for progress/completion but not for activity, year views or entry dates. Phase 4 Jellyfin code must use source `library.SourceJellyfin`. Phase 6 applies the same rule to migrated shows that started mid-series.
- AniList dates: keep an entry-specific completion date; a date shared by > 2 entries is a bulk fill-in date and ignored; series from 2012+ are dated by episode air date; older ones get 2019-01-01. CURRENT entries use AniList `updatedAt`.
- AniList entries map into TVDB seasons via the Fribb mapping (`season.tvdb` + `episode_offset.tvdb`; season 0 = specials): progress N of an entry marks episodes offset+1..offset+N of that TVDB season; long runners without a season start at S1 and spill by **position** to the next season; spill stops at a season another AniList entry claims; unplaceable entries stay count-based anime. Anime films stay TMDB `anime_movie`.
- A TVDB 404 is a miss, so IMDb/TMDB fallbacks run.

The Python implementation under `src/` is the reference for every behaviour
not written here. Port behaviour, not structure.

## Shows are TVDB (owner decision 2026-11-25)

- **Catalog.** A show item is `source='tvdb'`, kind `tv` or `anime` (the Fribb `thetvdb_id` decides; the user can override). Metadata, seasons and episodes come from TVDB v4 only: English title and overview (the translation endpoint when the default language is not English, the original name kept as `native_title`), poster, genres, status, network, year, and the **official/aired order** with season 0 = specials. `item_seasons` / `item_episodes` use TVDB numbers; `item_episodes.ext_id` holds the TVDB episode id. Cross ids (tmdb, imdb, mal, anilist) live in `item_ids` **only for matching** (Jellyfin, AniList, old links, the conversion), never as a metadata source for shows.
- **Cast and crew** are TVDB's characters (first 40 actors in TVDB order, plus creators, directors, writers up to 8), no TMDB credits and no AniList voice actors for shows. A TVDB person and a TMDB person are separate source records and are merged **only when they share a reliable external id**: the person's TVDB `remoteIds` list their TMDB person id, and then the credit points at that TMDB person record (which already has the film credits), keeping its name; otherwise the person is a `tvdb` record. Never by name.
- **Calendar/releases** come from TVDB episode air dates. TVDB has no change feed, so a show whose stored status is not `Ended` is re-synced at most daily by the 6-hourly job.
- **Search**: shows from TVDB search, split by the Fribb mapping into `tv` and `anime` (the anime search also asks AniList and places its hits on TVDB through the mapping); movies from TMDB. AniList hits with no TVDB series stay count-based anime; films are TMDB `anime_movie`.
- **Jellyfin**: the episode's TVDB id (catalog `ext_id`, else TVDB), else the series TVDB id with Jellyfin's season/episode numbers, else the episode's IMDb id through TVDB. No TMDB step for shows, and the old fallback that used TVDB/Jellyfin numbers as TMDB numbers is gone. Movies are unchanged (TMDB).
- **Provider limits**: TVDB calls go through the shared client (10 requests/s) and the provider cache (24 h); the login token is cached in memory and in `provider_cache` (28 days) so restarts and the CLI do not log in again. `TVDB_API` is required for shows.
- **Old links**: `/title/tv/tmdb/<id>`, `/shows/tmdb/<id>`, `/metadata/.../tmdb/<id>`, `/library/find?source=tmdb` resolve to the TVDB item (its `item_ids` keep the TMDB id; a TMDB id never seen before is resolved through TMDB's own TVDB cross id). The title page replaces the URL with the TVDB one.
- **Conversion** (`encompass shows-to-tvdb [--dry-run]`, `internal/showconv`, idempotent, one transaction per show): finds each TMDB show's TVDB series (stored TVDB id, TMDB external ids, the mapping, IMDb through TVDB), creates or merges into the TVDB item, moves entries (all runs; an entry landing on an existing TVDB entry becomes its next run), watches, `entry_seasons`, list items, reading links; remaps every watch (including `assumed`) from the TMDB (season, episode) to the TVDB one, trying in order exact ids (TMDB episode external ids, only for days the dates cannot settle), air date, a day apart, identical title, position between matched neighbours; specials in their own class. A watch with no twin is never dropped: it stays on the closest episode (nearest air date, else same number, else nearest number) with a note on the watch, and is listed in the report. Season rows are rebuilt from the remapped watches (Completed when every released episode is watched and none is still to air, else In progress; a Paused/Dropped TMDB season keeps that status; scores and notes go to the first TVDB season of the TMDB season). Entry status, score, dates and notes are untouched, and no history events are written; progress is recounted (distinct regular episodes). TMDB show credits are replaced by TVDB credits and people left without credits are removed. Shows with no TVDB match stay on TMDB (they still work: `HasEpisodes` is true for `tvdb` and `tmdb` shows) and are listed.
- Mapping data: `anime_mappings` gains `tvdb_season` / `tvdb_offset` (migration 00007 empties the table so the next load fills them).

## Architecture

```
server/
  cmd/encompass/main.go        flags/env, wiring, graceful shutdown
  internal/config              env config (same names as today where they exist)
  internal/db                  goose migrations (embedded), sqlc output, tx helpers
  internal/auth                sessions (cookie), API tokens (bearer), Django pbkdf2 verify → argon2id rehash, CSRF
  internal/jobs                in-process scheduler (interval/cron) + durable one-off job table, small worker pool
  internal/cache               provider response cache (SQLite table, TTL) + small in-memory LRU
  internal/providers/{tmdb,tvdb,anilist,mal,hardcover,openlibrary,mangaupdates,comicvine,igdb,bgg}
  internal/catalog             items, item ids, seasons/episodes refresh, anime mapping, people/credits
  internal/library             entries, watches, status rules (never-undo, additive), progress
  internal/reading             documents, page events, sessions, KOSync, KOReader, book linking, Triple P
  internal/integrations/{jellyfin,anilist,goodreads,steam}
  internal/calendar            iCal feed
  internal/lists               custom lists
  internal/httpapi             routing, JSON helpers, error shape, one file per area mirroring src/api/*.py
  internal/migrate             one-shot Django SQLite → new schema importer
  web/                         embedded SPA build (go:embed of frontend/build)
```

Rules: no global state except the logger; packages take `*db.Queries`/`*sql.DB`
and an `*http.Client`; every external call has a timeout and goes through
`cache`; context everywhere; `log/slog` JSON logs.

### HTTP surface (contract)

- `/api/v1/*` — same paths, params and JSON shapes as today (see
  `openapi-django.json` and `golden/`), except anime endpoints, which change
  with the anime model (phase 5 updates the SPA to match).
- `/kosync/*` — KOSync protocol, unchanged.
- `/webhook/jellyfin/{token}` — unchanged URL and payload handling (plain plugin template + legacy template).
- `/calendar/{token}.ics` — iCal feed.
- `/health` — liveness.
- Everything else → SPA (`200.html` fallback; `/_app/immutable/*` cached 1 year).

## Schema (summary; full DDL in `schema.sql`)

- `users`, `sessions`, `api_tokens`, `user_media_settings`
- Catalog: `items` (kind, title, image, year, …), `item_ids` (source + external id per item: tmdb, tvdb, imdb, anilist, mal, hardcover, openlibrary, igdb, bgg, steam), `item_seasons`, `item_episodes`, `anime_mappings`, `people`, `credits`, `credit_fetches`, `releases`
- Tracking: `entries` (user × item × run, status, score, progress, dates, notes), `watches` (episode/movie plays: season, episode, watched_at, source), `entry_events` (status/progress/score history for activity and year views)
- Reading: `reading_documents`, `page_events`, `reading_sessions`, `book_notes` (Triple P + Reflect per entry)
- Other: `lists`, `list_items`, `list_members`, `jobs`, `kv` (sync markers), `provider_cache`

## Phases

| Phase | Work | Who |
|---|---|---|
| 0 | This doc, `schema.sql`, exported OpenAPI, golden responses recorded from prod (done 2026-11-25: 110 responses + a prod DB snapshot, kept OUTSIDE the repo in `~/Projects/encompass-dev/` because they hold personal data; delete after cutover) | Opus |
| 1 | Skeleton: config, db + migrations + sqlc, auth (sessions, tokens, Django password verify), jobs, cache, SPA embedding, `/health`, Dockerfile + GH Actions → GHCR | Opus + 1 Sonnet |
| 2 | Providers (parallel): A tmdb+tvdb · B anilist+mal+anime mapping · C hardcover+openlibrary+mangaupdates · D igdb+bgg+comicvine | 4 Sonnet |
| 3 | API areas (parallel): A library/shows/seasons/episodes/anime · B reading/KOSync/KOReader/book linking/Triple P · C people/credits · D now/activity/years/summary/search/tokens/me | 4 Sonnet |
| 4 | Integrations: Jellyfin webhook + import, AniList import + poll, Goodreads CSV, Steam; calendar; lists | 2–3 Sonnet |
| 5 | SPA (mobile-first PWA, see Frontend section): Settings (media toggles, password, tokens, users), per-type Sources page, Link your books, board games, anime/anime movie pages, Triple P | 1–2 Sonnet |
| 6 | Django → Go data migration, golden comparison, side-by-side run, cutover, decommission Django | Opus + 1 Sonnet |

Each phase: tests pass (`go test ./...`, `go vet`, `staticcheck`), golden
comparison for touched endpoints, Opus review before merge. Agents work in
their own worktrees on `rewrite/<phase>-<area>` branches off `rewrite/main`.

## Frontend: one codebase, installable PWA, mobile-first (owner requirement)

- One codebase: the SvelteKit SPA in `frontend/` is built into `server/web/dist` and embedded in the Go binary; one image, one container. No separate frontend deploy, no native apps.
- PWA: installable (manifest, maskable icons, `display: standalone`, shortcuts), service worker that precaches the app shell and serves cached API GETs when offline (stale-while-revalidate for library/now/item pages), with an "offline" indicator. Episode/progress taps made offline are queued and replayed in order when back online (manual source, same rules).
- Mobile-first, designed for a phone held in one hand, then scaled up to desktop:
  - bottom tab bar on phones (Now, Library, Search, Reading, More), sidebar on wide screens; respects safe areas and the iOS home indicator;
  - tap targets ≥ 44 px, primary actions in thumb reach (mark next episode, +1 page/chapter, rate) without opening menus; swipe/long-press are shortcuts, never the only way;
  - poster-grid and list views, sticky filter chips that scroll horizontally, bottom sheets instead of modals, pull-to-refresh;
  - fast: route-level code splitting, lazy images with fixed aspect ratios (no layout shift), skeletons not spinners, optimistic updates;
  - dark by default with light theme support, system font stack, readable at 375 px width with no horizontal scroll;
  - every screen checked on a 375×812 viewport (and installed-PWA mode) before merge.

## Conventions for agents

- Read this doc, `schema.sql`, and the Python reference files named in your task before writing code.
- Port behaviour exactly, including edge cases in the Python tests; port the relevant tests too.
- SQL lives in `internal/db/queries/*.sql` (sqlc). No string-built SQL.
- No new dependencies without listing them in your report. Allowed: stdlib, `modernc.org/sqlite`, `pressly/goose/v3`, `golang.org/x/crypto`, `golang.org/x/sync`. Ask for anything else.
- Every route that needs a logged-in user goes through `auth.Require` / `auth.RequireWrite`: that is where CSRF is enforced for cookie sessions. Never read `auth.User(ctx)` on an unwrapped route.
- Never hard-code hostnames, IPs, domains or keys: env config only.
- Don't touch files outside your package except `internal/db/queries/<area>.sql` and `internal/httpapi/<area>.go`.
- Report: what you built, what you left out and why, test results, any behaviour you weren't sure of.

## Integration notes from earlier phases (pick these up)

- Phase 3 wiring: call `mappings.New(...).RegisterJobs(jobs)` in `main.go`; each provider has `Metadata(...)`/`Search(...)` (or `Game`/`Boardgame`/`Comic`) — phase 3 builds the dispatch by (kind, source) that `services.get_media_metadata`/`search` did.
- Anime are TVDB shows (superseded the earlier TMDB-structured rule, see "Shows are TVDB"); Anime Movie is a TMDB film. AniList/MAL drive mapping/imports only. MAL/AniList-only "count" anime exist solely for titles with no TVDB (or TMDB film) match. The AniList/MAL clients only know `anime`/`manga`; the catalog maps `anime_movie` to TMDB movies.
- The TMDB+AniList episode merge (`catalog/anime.py: anime_episodes`) belongs to phase 3A.
- (Superseded by the Jellyfin note under "Shows are TVDB".) Phase 4 Jellyfin: the anibridge helpers in `src/integrations/webhooks/anime_mappings.py` (AniDB/TVDB/TMDB-movie/IMDb → MAL, separate `mappings.min.json` dataset) are only needed when ANIME_AS_TV=False; with anime tracked as TMDB shows they can be dropped — confirm before porting.
- Provider timestamps: MAL `broadcast` strings depend on TZ (golden recorded in America/Toronto).
- IGDB token lives in memory, not in `provider_cache`. The TVDB token is cached in both (see "Shows are TVDB").
