"""Media Now: what the user is currently consuming, per media type."""

import logging

from django.apps import apps
from ninja import Router

from api.serializers import item_dict, jsonable
from app.models import MediaTypes, Status
from app.providers import services
from reading.models import ReadingSession

logger = logging.getLogger(__name__)
router = Router(tags=["now"])

# TV is represented by its in-progress season (episodes are what you watch).
NOW_TYPES = [
    MediaTypes.SEASON.value,
    MediaTypes.ANIME.value,
    MediaTypes.MANGA.value,
    MediaTypes.BOOK.value,
    MediaTypes.COMIC.value,
    MediaTypes.MOVIE.value,
    MediaTypes.GAME.value,
    MediaTypes.BOARDGAME.value,
]


def _metadata(item):
    try:
        return services.get_media_metadata(
            item.media_type,
            item.media_id,
            item.source,
            [item.season_number] if item.season_number is not None else None,
        )
    except Exception:  # noqa: BLE001 a provider outage must not break the page
        logger.warning("No metadata for %s", item)
        return {}


def _next_episode(meta, progress):
    for episode in meta.get("episodes") or []:
        number = episode.get("episode_number")
        if number and number > progress:
            return {
                "number": number,
                "title": episode.get("name") or episode.get("title"),
                "air_date": episode.get("air_date"),
            }
    return None


def now_entry(entry, media_type):
    """Serialize an in-progress entry with totals and what comes next."""
    item = entry.item
    meta = _metadata(item)
    progress = entry.progress
    total = meta.get("max_progress")
    data = {
        "id": entry.id,
        "media_type": media_type,
        "item": item_dict(item),
        "title": item.title,
        "image": meta.get("image") or item.image,
        "progress": progress,
        "total": total,
        "percent": round(progress / total * 100) if total else None,
        "score": jsonable(entry.score),
        "progressed_at": jsonable(entry.progressed_at),
        "start_date": jsonable(entry.start_date),
        "genres": jsonable(meta.get("genres") or []),
        "synopsis": meta.get("synopsis"),
        "next": None,
    }
    if media_type == MediaTypes.SEASON.value:
        data["tv_id"] = entry.related_tv_id
        data["season_number"] = item.season_number
        data["next"] = _next_episode(meta, progress)
    elif media_type in (MediaTypes.ANIME.value, MediaTypes.MANGA.value):
        if total is None or progress < total:
            data["next"] = {"number": progress + 1, "title": None}
    elif media_type == MediaTypes.BOOK.value:
        last = (
            ReadingSession.objects.filter(user=entry.user, item=item)
            .order_by("-started_at")
            .first()
        )
        if last:
            data["last_session"] = {
                "started_at": jsonable(last.started_at),
                "pages_read": last.pages_read,
                "minutes": round(last.duration_seconds / 60),
            }
    return data


@router.get("/now")
def media_now(request):
    """In-progress entries grouped by media type, most recently touched first."""
    groups = []
    for media_type in NOW_TYPES:
        model = apps.get_model(app_label="app", model_name=media_type)
        # Seasons derive progress from episodes, so sort in Python.
        entries = list(
            model.objects.filter(
                user=request.user,
                status=Status.IN_PROGRESS.value,
            ).select_related("item"),
        )
        if not entries:
            continue
        entries.sort(key=lambda e: e.progressed_at or e.created_at, reverse=True)
        groups.append(
            {
                "media_type": "tv"
                if media_type == MediaTypes.SEASON.value
                else media_type,
                "entries": [now_entry(e, media_type) for e in entries[:12]],
            },
        )
    groups.sort(
        key=lambda g: max(e["progressed_at"] or "" for e in g["entries"]),
        reverse=True,
    )
    return {"groups": groups}
