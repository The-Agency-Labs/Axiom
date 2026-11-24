# Fork additions

This fork of [Yamtrack](https://github.com/FuzzyGrim/Yamtrack) (AGPL-3.0) adds a
JSON API, reading sessions and device integrations. Upstream's features are kept.

| Piece | Where |
|---|---|
| JSON API + API tokens | `src/api/` → `/api/v1/` (docs at `/api/v1/docs`) |
| Reading sessions, KOSync server | `src/reading/` → `/kosync/` |
| KOReader plugin | `clients/koreader/mediatracker.koplugin/` |

## API tokens

Tokens are named, revocable and either `read` or `write`. Only hashes are stored;
the key is shown once.

```sh
# inside the container
python manage.py create_api_token <username> kindle --scope write
python manage.py create_api_token <username> assistant            # read-only
```

From the browser (logged in) you can also `POST /api/v1/tokens`.

Send it as `Authorization: Bearer <key>`.

## Personal assistant

Give the assistant a **read-only** token and the OpenAPI schema at
`/api/v1/openapi.json`. Useful calls:

| Question | Endpoint |
|---|---|
| Did I read today? Streak? Pages this week? | `GET /api/v1/reading/stats?start=YYYY-MM-DD&end=YYYY-MM-DD` |
| What am I watching/reading right now? | `GET /api/v1/summary` |
| What did I do last week? | `GET /api/v1/activity?start=…&end=…` (page with `next_cursor`) |
| Quiz material for one title | `GET /api/v1/library/{media_type}/{id}`: synopsis/metadata, your notes, history, episodes watched |
| Find a title in the library | `GET /api/v1/library?search=frieren` |
| Reading sessions | `GET /api/v1/reading/sessions` |

## Kindle (jailbroken, KOReader)

1. **Tailscale** on the Kindle (KUAL extension, userspace networking) so it can
   reach the server over the tailnet.
2. **Position sync:** KOReader → Tools → Progress sync → Custom sync server:
   `https://<server>/kosync`. Log in with your username and a **write** token
   as the password. Registration from the device is disabled on purpose.
3. **Session stats:** copy `clients/koreader/mediatracker.koplugin` to
   `koreader/plugins/` on the Kindle, restart KOReader, then
   Tools → Media tracker sync → set Server URL and API token.
   Make sure KOReader's Reading statistics plugin is enabled.

The plugin uploads on suspend, book close and exit when Wi-Fi is up, and again on
wake / Wi-Fi connect, so nothing is lost if Wi-Fi was already off. The server
dedupes page views and groups them into sessions (a 10-minute pause starts a new
one).

4. **Link books:** KOReader identifies files by hash, not ISBN. New books show up
   in `GET /api/v1/reading/documents?unlinked=true`; pick a match from
   `…/documents/{id}/suggestions` and `POST …/documents/{id}/link`
   `{"source": "hardcover", "media_id": "…"}`. After that, every sync updates
   the book's progress (scaled to the edition's page count) and status.

## Jellyfin

Upstream's webhook already works: install the Jellyfin **Webhook** plugin and
point a Generic destination at `https://<server>/webhook/jellyfin/<user token>`
(token from Settings → Integrations).
