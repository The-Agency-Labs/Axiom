# Encompass

A self-hosted tracker for everything you watch and read: TV, anime, movies,
manga, books, comics, games and board games. One small Go binary with SQLite
and an installable, mobile-first web app (PWA).

Originally a fork of [Yamtrack](https://github.com/FuzzyGrim/Yamtrack) by
FuzzyGrim; rewritten in Go. Licensed AGPL-3.0.

## Features

- **Library:** status, score, progress, rewatches/rereads, dates and notes for
  every media type. Anime and anime films are tracked as whole TMDB shows and
  films, with seasons and specials.
- **Now:** one-tap "watched next episode" and +1 steppers for what you're in
  the middle of.
- **Imports and sync (additive, never lowers progress):**
  - Jellyfin webhook (marked watched at 75 %) and a 6-hourly history sync. A
    show first seen at season N assumes earlier seasons were watched.
  - AniList polling every 30 minutes, Goodreads CSV, Steam.
- **Reading:** KOReader sync (KOSync API + the plugin in
  `clients/koreader`), reading sessions, a daily reading log, stats, and
  linking Kindle documents to books (Hardcover, OpenLibrary fallback).
- **Books:** series with your place in them, search by series, and Triple P +
  Reflect notes.
- **Also:** calendar (with an `.ics` feed), lists, people, year in review.
- **Offline:** the app opens with your last data and queues changes until
  you're back online.

## Running it

The image `ghcr.io/the-agency-labs/encompass` is built by GitHub Actions.
See [`server/deploy/compose.yaml`](server/deploy/compose.yaml):

```sh
mkdir -p data && sudo chown 65532:65532 data
docker compose up -d
docker compose exec -T encompass /encompass createuser <name> --admin <<< "<password>"
```

Settings come from `.env`:

| Variable | Purpose |
|---|---|
| `SECRET` | Long random string (sessions, CSRF) |
| `URLS` | Public URL(s), comma separated |
| `TZ` | IANA time zone (day boundaries, stats) |
| `DATABASE_PATH` | SQLite file (default under `/data`) |
| `REGISTRATION` | Allow sign-ups (default off) |
| `TMDB_API`, `TVDB_API`, `MAL_API`, `IGDB_ID`, `IGDB_SECRET`, `HARDCOVER_API`, `COMICVINE_API`, `BGG_API_TOKEN`, `STEAM_API_KEY` | Provider keys |
| `TMDB_LANG`, `*_NSFW` | Provider language and adult-content filters |
| `JELLYFIN_URL`, `JELLYFIN_API_KEY`, `JELLYFIN_USERS` (`<jellyfin>:<encompass>`), `JELLYFIN_WATCHED_PERCENT` | Jellyfin history sync |
| `ANILIST_USERS` (`<anilist>:<encompass>`) | AniList polling |
| `SESSION_DAYS` | Login lifetime |

Coming from Yamtrack or the old Django Encompass:
`encompass migrate-django --from <old db.sqlite3> --timezone <zone>`
(`--dry-run` first).

## Development

- `server/`: Go (stdlib `net/http`, `modernc.org/sqlite`, goose migrations,
  sqlc). `go test ./...` from `server/`.
- `frontend/`: SvelteKit (Svelte 5, static adapter, Tailwind). `pnpm dev`;
  `pnpm build` writes `frontend/build`, which the Docker build copies into
  `server/web/dist` and embeds in the binary.
- Design notes: [`docs/rewrite/PLAN.md`](docs/rewrite/PLAN.md).
