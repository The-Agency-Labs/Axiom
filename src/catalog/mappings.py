"""Anime ID mappings from the open Fribb/anime-lists dataset.

The dataset links MyAnimeList / AniList IDs to TMDB and TVDB, including the
TMDB season and episode offset of each cour. It's stored as a table (not
held in memory) and refreshed weekly.
"""

import datetime
import logging

import requests
from django.conf import settings
from django.core.cache import cache
from django.db import transaction
from django.utils import timezone

from catalog.models import AnimeMapping

logger = logging.getLogger(__name__)

DATASET_URL = (
    "https://raw.githubusercontent.com/Fribb/anime-lists/master/anime-list-full.json"
)
REFRESHED_KEY = "catalog:anime_mappings_refreshed"
MAX_AGE = datetime.timedelta(days=7)


def _int(value):
    if isinstance(value, list):
        value = value[0] if value else None
    try:
        return int(value) if value is not None else None
    except (TypeError, ValueError):
        return None


def _row(entry):
    tmdb = entry.get("themoviedb_id") or {}
    if not isinstance(tmdb, dict):
        tmdb = {}
    season = entry.get("season") or {}
    offset = entry.get("episode_offset") or {}
    return AnimeMapping(
        mal_id=_int(entry.get("mal_id")),
        anilist_id=_int(entry.get("anilist_id")),
        kind=str(entry.get("type") or "")[:20],
        tmdb_tv=_int(tmdb.get("tv")),
        tmdb_movie=_int(tmdb.get("movie")),
        tmdb_season=_int(season.get("tmdb")),
        tmdb_offset=_int(offset.get("tmdb")) or 0,
        tvdb_id=_int(entry.get("tvdb_id")),
    )


def refresh(data=None):
    """Replace the mapping table with the latest dataset. Returns the row count."""
    if data is None:
        response = requests.get(DATASET_URL, timeout=max(settings.REQUEST_TIMEOUT, 60))
        response.raise_for_status()
        data = response.json()
    rows = [
        _row(entry)
        for entry in data
        if isinstance(entry, dict) and (entry.get("mal_id") or entry.get("anilist_id"))
    ]
    with transaction.atomic():
        AnimeMapping.objects.all().delete()
        AnimeMapping.objects.bulk_create(rows, batch_size=2000)
    cache.set(REFRESHED_KEY, timezone.now().isoformat(), None)
    logger.info("Loaded %d anime mappings", len(rows))
    return len(rows)


def ensure_loaded():
    """Load the dataset the first time it's needed."""
    if AnimeMapping.objects.exists():
        return
    try:
        refresh()
    except (requests.RequestException, ValueError):
        logger.warning("Anime mappings unavailable", exc_info=True)


def is_stale():
    """True when the table is empty or older than a week."""  # noqa: D401
    stamp = cache.get(REFRESHED_KEY)
    if not stamp or not AnimeMapping.objects.exists():
        return True
    return timezone.now() - datetime.datetime.fromisoformat(stamp) > MAX_AGE


def lookup(source, media_id):
    """Find the mapping for a MAL or AniList anime ID, or None."""
    ensure_loaded()
    media_id = _int(media_id)
    if media_id is None:
        return None
    field = "anilist_id" if source == "anilist" else "mal_id"
    return AnimeMapping.objects.filter(**{field: media_id}).order_by("id").first()
