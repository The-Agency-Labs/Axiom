"""Background jobs: keep anime mappings fresh and backfill library cast."""

import logging
import time

from celery import shared_task
from django.apps import apps

from app.models import Item, MediaTypes
from catalog import credits, mappings  # noqa: A004

logger = logging.getLogger(__name__)

# Models whose items can have cast.
CAST_TYPES = [MediaTypes.TV.value, MediaTypes.MOVIE.value, MediaTypes.ANIME.value]
PAUSE_SECONDS = 0.4  # stay well under provider rate limits


def library_items(user=None):
    """Items in anyone's (or one user's) library that can have cast."""
    ids = set()
    for kind in CAST_TYPES:
        queryset = apps.get_model("app", kind).objects.all()
        if user is not None:
            queryset = queryset.filter(user=user)
        ids.update(queryset.values_list("item_id", flat=True))
    return Item.objects.filter(id__in=ids).select_related("credit_fetch")


@shared_task(name="Backfill library cast")
def backfill_credits(limit=200, user_id=None):
    """Fetch cast for library items that have none yet."""
    user = None
    if user_id is not None:
        user = apps.get_model("users", "User").objects.filter(id=user_id).first()
    missing = credits.missing_items(library_items(user))[:limit]
    for item in missing:
        credits.fetch(item)
        time.sleep(PAUSE_SECONDS)
    logger.info("Fetched cast for %d items", len(missing))
    return len(missing)


@shared_task(name="Refresh anime mappings")
def refresh_anime_mappings():
    """Reload the MAL/AniList -> TMDB mapping dataset when it's a week old."""
    if mappings.is_stale():
        return mappings.refresh()
    return 0
