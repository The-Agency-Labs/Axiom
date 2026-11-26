# Axiom

A self-hosted tracker for everything you watch and read: TV, anime, movies,
manga, books, comics, games and board games. One small Go binary with SQLite
and an installable, mobile-first web app (PWA).

Licensed AGPL-3.0.

## Features

- **Library:** status, score, progress, rewatches/rereads, dates and notes for
  every media type. Shows, anime included, come from TheTVDB (its aired
  seasons and specials, which is also how Jellyfin numbers them); movies and
  anime films come from TMDB. Shows need `TVDB_API`.
- **Now:** one-tap "watched next episode" and +1 steppers for what you're in
  the middle of.
- **Imports and sync (additive, never lowers progress):**
  - Jellyfin webhook (marked watched at 75 %) and a 6-hourly history sync. A
    show first seen at season N assumes earlier seasons were watched.
  - AniList polling every 30 minutes, MyAnimeList list import, Goodreads CSV,
    Steam. All under Settings → Integrations.
- **Reading:** KOReader sync (KOSync API + the plugin in
  `clients/koreader`), reading sessions, a daily reading log, stats, and
  linking Kindle documents to books (Hardcover, OpenLibrary fallback).
- **Books:** series with your place in them, search by series, and Triple P +
  Reflect notes.
- **Also:** upcoming releases (with an `.ics` feed) and seasonal anime, people
  (with character pictures), year in review.
- **Offline:** the app opens with your last data and queues changes until
  you're back online.

## Running it

The image `ghcr.io/the-agency-labs/axiom` is built by GitHub Actions.
See [`server/deploy/compose.yaml`](server/deploy/compose.yaml):

```sh
mkdir -p data && sudo chown 65532:65532 data
docker compose up -d
docker compose exec -T axiom /axiom createuser <name> --admin <<< "<password>"
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
| `JELLYFIN_URL`, `JELLYFIN_API_KEY`, `JELLYFIN_USERS` (`<jellyfin>:<axiom>`), `JELLYFIN_WATCHED_PERCENT` | Jellyfin history sync |
| `ANILIST_USERS` (`<anilist>:<axiom>`) | AniList polling |
| `SESSION_DAYS` | Login lifetime |

Coming from the old Django version of Axiom:
`axiom migrate-django --from <old db.sqlite3> --timezone <zone>`
(`--dry-run` first).

Shows tracked from TMDB (before shows moved to TheTVDB) are converted in place
with `axiom shows-to-tvdb --dry-run`, then without `--dry-run`: back up the
database first; it needs `TVDB_API` and `TMDB_API`, is safe to repeat and reports
every watch it had to place on the closest episode.

## Development

- `server/`: Go (stdlib `net/http`, `modernc.org/sqlite`, goose migrations,
  sqlc). `go test ./...` from `server/`.
- `frontend/`: SvelteKit (Svelte 5, static adapter, Tailwind). `pnpm dev`;
  `pnpm build` writes `frontend/build`, which the Docker build copies into
  `server/web/dist` and embeds in the binary.
- Design notes: [`docs/rewrite/PLAN.md`](docs/rewrite/PLAN.md).
