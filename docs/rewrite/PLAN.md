# Encompass server rewrite (Django → Go)

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
| Media types | `tv`, `anime`, `movie`, `anime_movie`, `manga`, `comic`, `book`, `game`, `boardgame`; each can be toggled per user (owner has comics off). Anime and Anime Movie are their own entry types, TMDB-structured (seasons/specials) with AniList/MAL data attached. |
| Anime classification | Automatic on add via the Fribb mapping (TMDB id → anime); user can override per item. |
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
- A show first seen at season N does not create an empty season 1.
- AniList dates: keep an entry-specific completion date; a date shared by > 2 entries is a bulk fill-in date and ignored; series from 2012+ are dated by episode air date; older ones get 2019-01-01. CURRENT entries use AniList `updatedAt`.
- AniList entries map into TMDB seasons via the Fribb mapping (season + offset); long runners without a season start at S1 and spill by **position** (TMDB numbers some seasons continuously); spill stops at a season another AniList entry claims; unplaceable entries stay count-based anime.
- A TVDB 404 is a miss, so IMDb/TMDB fallbacks run.

The Python implementation under `src/` is the reference for every behaviour
not written here. Port behaviour, not structure.

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
| 5 | SPA: Settings (media toggles, password, tokens, users), per-type Sources page, Link your books, board games, anime/anime movie pages, Triple P | 1–2 Sonnet |
| 6 | Django → Go data migration, golden comparison, side-by-side run, cutover, decommission Django | Opus + 1 Sonnet |

Each phase: tests pass (`go test ./...`, `go vet`, `staticcheck`), golden
comparison for touched endpoints, Opus review before merge. Agents work in
their own worktrees on `rewrite/<phase>-<area>` branches off `rewrite/main`.

## Conventions for agents

- Read this doc, `schema.sql`, and the Python reference files named in your task before writing code.
- Port behaviour exactly, including edge cases in the Python tests; port the relevant tests too.
- SQL lives in `internal/db/queries/*.sql` (sqlc). No string-built SQL.
- No new dependencies without listing them in your report. Allowed: stdlib, `modernc.org/sqlite`, `pressly/goose/v3`, `golang.org/x/crypto`, `golang.org/x/sync`. Ask for anything else.
- Never hard-code hostnames, IPs, domains or keys: env config only.
- Don't touch files outside your package except `internal/db/queries/<area>.sql` and `internal/httpapi/<area>.go`.
- Report: what you built, what you left out and why, test results, any behaviour you weren't sure of.
