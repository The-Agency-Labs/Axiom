"""Per-episode data for anime, which MAL/AniList don't provide in full.

Resolution order, merged per episode number:
1. TMDB, via the anime-lists mapping (show, season and episode offset):
   titles, synopses, stills, air dates, runtimes and ratings.
2. AniList streaming episodes (Crunchyroll etc.): titles and thumbnails.
3. Plain numbered episodes up to the known episode count.
"""

import logging
import re

from django.core.cache import cache

from app.models import MediaTypes, Sources
from app.providers import anilist, services
from catalog import mappings

logger = logging.getLogger(__name__)

TMDB_STILL = "https://image.tmdb.org/t/p/w500"
CACHE_SECONDS = 60 * 60 * 24
STREAMING_TITLE = re.compile(
    r"^\s*(?:Episode|Ep\.?)\s*(\d+)\s*[-:–]?\s*(.*)$",  # noqa: RUF001 en dash in titles
    re.IGNORECASE,
)

ANILIST_QUERY = """
query ($id: Int, $idMal: Int) {
  Media(id: $id, idMal: $idMal, type: ANIME) {
    id
    idMal
    episodes
    streamingEpisodes { title thumbnail site }
  }
}
"""


def _anilist_media(source, media_id):
    key = f"catalog:anilist_episodes:{source}:{media_id}"
    data = cache.get(key)
    if data is None:
        variables = (
            {"id": int(media_id)}
            if source == Sources.ANILIST.value
            else {"idMal": int(media_id)}
        )
        try:
            data = anilist.graphql_request(ANILIST_QUERY, variables)["Media"] or {}
        except (services.ProviderAPIError, KeyError, TypeError, ValueError):
            logger.warning("AniList episodes unavailable for %s %s", source, media_id)
            data = {}
        cache.set(key, data, CACHE_SECONDS)
    return data


def _streaming_episodes(media):
    """Map episode number -> (title, thumbnail) from AniList streaming links."""
    episodes = {}
    for index, ep in enumerate(media.get("streamingEpisodes") or [], start=1):
        match = STREAMING_TITLE.match(ep.get("title") or "")
        number, title = (
            (int(match.group(1)), match.group(2).strip())
            if match
            else (index, ep.get("title"))
        )
        # Several sites can list the same episode; keep the first.
        episodes.setdefault(
            number, {"title": title or None, "image": ep.get("thumbnail")}
        )
    return episodes


def _tmdb_episodes(mapping, count):
    """TMDB episodes for this cour, renumbered from 1."""
    if (
        not mapping
        or not mapping.tmdb_tv
        or mapping.tmdb_season is None
        or mapping.kind.upper() == "MOVIE"
    ):
        return {}, None
    try:
        season = services.get_media_metadata(
            MediaTypes.SEASON.value,
            str(mapping.tmdb_tv),
            Sources.TMDB.value,
            [mapping.tmdb_season],
        )
    except services.ProviderAPIError:
        logger.warning("TMDB season unavailable for mapping %s", mapping.pk)
        return {}, None
    raw = sorted(season.get("episodes", []), key=lambda e: e["episode_number"])
    raw = [e for e in raw if e["episode_number"] > mapping.tmdb_offset]
    if count:
        raw = raw[:count]
    episodes = {}
    for number, ep in enumerate(raw, start=1):
        still = ep.get("still_path")
        episodes[number] = {
            "title": ep.get("name"),
            "overview": ep.get("overview") or "",
            "image": f"{TMDB_STILL}{still}" if still else None,
            "air_date": ep.get("air_date"),
            "runtime": ep.get("runtime"),
            "score": ep.get("vote_average") or None,
            "kind": ep.get("episode_type") or "standard",
        }
    link = {
        "tmdb_id": mapping.tmdb_tv,
        "season_number": mapping.tmdb_season,
        "episode_offset": mapping.tmdb_offset,
    }
    return episodes, link


def anime_episodes(source, media_id, episode_count=None):
    """Return ``(episodes, info)`` for an anime from MAL or AniList.

    Each episode has number, title, overview, image, air_date, runtime,
    score and kind; missing fields are None. ``info`` names the providers.
    """
    mapping = mappings.lookup(source, media_id)
    tmdb, link = _tmdb_episodes(mapping, episode_count)
    media = _anilist_media(source, media_id)
    streaming = _streaming_episodes(media)

    count = episode_count or media.get("episodes") or max([*tmdb, *streaming, 0])
    numbers = range(1, count + 1) if count else sorted({*tmdb, *streaming})
    episodes = []
    for number in numbers:
        base = tmdb.get(number, {})
        extra = streaming.get(number, {})
        episodes.append(
            {
                "episode_number": number,
                "title": base.get("title") or extra.get("title") or f"Episode {number}",
                "overview": base.get("overview") or "",
                "image": base.get("image") or extra.get("image"),
                "air_date": base.get("air_date"),
                "runtime": base.get("runtime"),
                "score": base.get("score"),
                "kind": base.get("kind") or "standard",
            },
        )
    providers = [
        name for name, used in (("tmdb", tmdb), ("anilist", streaming)) if used
    ]
    return episodes, {
        "providers": providers,
        "tmdb": link,
        "anilist_id": media.get("id") or (mapping.anilist_id if mapping else None),
    }
