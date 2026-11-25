<!-- --8<-- [start:docs-index-intro] -->

# Encompass

A self-hosted tracker for everything you watch and read: shows, movies, anime,
manga, books, comics and games. Built by Agency Labs on top of
[Yamtrack](https://github.com/FuzzyGrim/Yamtrack) by FuzzyGrim.

![License](https://img.shields.io/badge/license-AGPL--3.0-blue)

<!-- --8<-- [end:docs-index-intro] -->

<!-- --8<-- [start:docs-index-body] -->

## What's in it

Everything from Yamtrack:

- Track movies, TV (per season and episode), anime, manga, games, books, comics
  and board games, with score, status, progress, rewatches, dates and notes.
- A full tracking history, custom entries, personal and shared lists, a release
  calendar (iCal) and Apprise notifications.
- Metadata from TMDB, TVDB, AniList, MyAnimeList, MangaUpdates, Hardcover,
  Open Library, IGDB, Comic Vine and BoardGameGeek.
- Jellyfin, Plex and Emby webhooks; imports from Trakt, Simkl, MyAnimeList,
  AniList, Kitsu, Goodreads, IMDb, HowLongToBeat and Steam; CSV export.
- Multi-user, OIDC and social login, SQLite or PostgreSQL.

Added in Encompass:

- **JSON API** at `/api/v1` (OpenAPI schema at `/api/v1/openapi.json`, docs at
  `/api/v1/docs`) with named, revocable read-only or read-write tokens.
- **Media Now** (`/api/v1/now`): what you're currently on in each media type,
  with totals and the next episode or chapter.
- **Reading sessions**: pages and minutes per session and per day, streaks.
- **KOReader sync**: a KOSync-compatible server at `/kosync` and a KOReader
  plugin that uploads reading statistics from a Kindle.
- **Assistant endpoints**: a summary, an activity feed and full per-title detail
  (synopsis, your notes, history, episodes watched) for a personal assistant.

## Run it

```sh
docker compose up -d        # SQLite
# or: docker compose -f docker-compose.postgres.yml up -d
```

Set `SECRET` to a long random string and `URLS` to the address you reach it on.
Keep hosts, keys and passwords in a `.env` file, not in the compose file. The
environment variables are the same as
[Yamtrack's](https://fuzzygrim.github.io/Yamtrack/) (some still carry the
`YAMTRACK_` prefix).

### Local preview without Redis

```sh
uv sync
cd src
export DJANGO_SETTINGS_MODULE=config.preview_settings SECRET=dev
python manage.py migrate
python manage.py seed_demo          # demo / demo-password, real titles
python manage.py runserver
```

## API tokens

```sh
python manage.py create_api_token <username> kindle --scope write
python manage.py create_api_token <username> assistant            # read-only
```

Or `POST /api/v1/tokens` from a logged-in browser. Send the key as
`Authorization: Bearer <key>`. Only hashes are stored; the key is shown once.

## Personal assistant

Give the assistant a **read-only** token and the OpenAPI schema.

| Question | Endpoint |
|---|---|
| What am I on right now? | `GET /api/v1/now` |
| Did I read today? Streak? Pages this week? | `GET /api/v1/reading/stats?start=YYYY-MM-DD&end=YYYY-MM-DD` |
| Overview: in progress, recently finished, reading habits | `GET /api/v1/summary` |
| What did I do last week? | `GET /api/v1/activity?start=…&end=…` (page with `next_cursor`) |
| Quiz material for one title | `GET /api/v1/library/{media_type}/{id}` |
| Find a title | `GET /api/v1/library?search=frieren` |
| Reading sessions | `GET /api/v1/reading/sessions` |

## Kindle (jailbroken, KOReader)

1. Run **Tailscale** on the Kindle (KUAL extension, userspace networking) so it
   can reach Encompass over the tailnet.
2. **Position sync:** KOReader → Tools → Progress sync → Custom sync server:
   `https://<server>/kosync`. Log in with your username and a **write** token as
   the password. Registering from the device is disabled on purpose.
3. **Session stats:** copy `clients/koreader/encompass.koplugin` to
   `koreader/plugins/` on the Kindle, restart KOReader, then
   Tools → Encompass sync → set the server URL and token. KOReader's Reading
   statistics plugin must be enabled.

   The plugin uploads on suspend, book close and exit when Wi-Fi is up, and
   again on wake and Wi-Fi connect, so nothing is lost if Wi-Fi was already off.
   The server ignores duplicates and starts a new session after a 10-minute
   pause.
4. **Link each new book once:** KOReader identifies files by hash, not ISBN.
   Unlinked books are listed at `GET /api/v1/reading/documents?unlinked=true`;
   pick a match from `…/documents/{id}/suggestions` and
   `POST …/documents/{id}/link` with `{"source": "hardcover", "media_id": "…"}`.
   From then on each sync updates the book's progress (scaled to the edition's
   page count) and status.

## Jellyfin

Install Jellyfin's **Webhook** plugin and add a Generic destination pointing at
`https://<server>/webhook/jellyfin/<token>` (token from Settings → Integrations).

## License and credit

AGPL-3.0, like Yamtrack. Encompass is a modified version of
[Yamtrack](https://github.com/FuzzyGrim/Yamtrack) © FuzzyGrim and contributors;
see [LICENSE](LICENSE). If you run a modified copy for others, the AGPL requires
offering them its source.

<!-- --8<-- [end:docs-index-body] -->
