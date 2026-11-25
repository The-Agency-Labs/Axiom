"""Anime episodes: list them like TV, and mark progress episode by episode.

Anime progress is a single count (as on MAL/AniList), so marking episode N
watched sets progress to N and unmarking sets it to N - 1. Per-episode watch
dates come from the entry's change history.
"""

from django.utils import timezone
from ninja import Router, Schema
from ninja.errors import HttpError

from api.auth import require_write
from api.serializers import jsonable, media_dict
from app.models import Anime, Item, MediaTypes, Sources, Status
from app.providers import services
from catalog.anime import anime_episodes

router = Router(tags=["anime"])
ANIME_SOURCES = {Sources.MAL.value, Sources.ANILIST.value}


def _check(source):
    if source not in ANIME_SOURCES:
        raise HttpError(400, "Anime episodes need a MyAnimeList or AniList id")


def _entry(user, source, media_id):
    return (
        Anime.objects.filter(user=user, item__source=source, item__media_id=media_id)
        .select_related("item")
        .first()
    )


def _watch_dates(entry):
    """Episode number -> when progress first reached it (most recent pass)."""
    dates = {}
    if entry is None:
        return dates
    previous = 0
    for record in entry.history.order_by("history_date").only(
        "history_date", "progress"
    ):
        progress = record.progress or 0
        if progress > previous:
            for number in range(previous + 1, progress + 1):
                dates[number] = record.history_date
        elif progress < previous:
            for number in range(progress + 1, previous + 1):
                dates.pop(number, None)
        previous = progress
    return dates


def _payload(user, source, media_id):
    meta = services.get_media_metadata(MediaTypes.ANIME.value, media_id, source)
    entry = _entry(user, source, media_id)
    episodes, info = anime_episodes(source, media_id, meta.get("max_progress"))
    progress = entry.progress if entry else 0
    dates = _watch_dates(entry)
    today = timezone.localdate().isoformat()
    for ep in episodes:
        ep["watched"] = ep["episode_number"] <= progress
        ep["watch_count"] = 1 if ep["watched"] else 0
        ep["watched_at"] = (
            jsonable(dates.get(ep["episode_number"])) if ep["watched"] else None
        )
        ep["aired"] = not ep["air_date"] or ep["air_date"] <= today
    return {
        "source": source,
        "media_id": media_id,
        "title": meta.get("title"),
        "entry": media_dict(entry, MediaTypes.ANIME.value) if entry else None,
        "progress": progress,
        "total": meta.get("max_progress") or len(episodes),
        "runtime_minutes": sum(ep["runtime"] or 0 for ep in episodes),
        "episodes": episodes,
        **info,
    }


@router.get("/anime/{source}/{media_id}/episodes")
def episodes(request, source: str, media_id: str):
    """Every episode with title, synopsis and still where providers have them."""
    _check(source)
    try:
        return _payload(request.user, source, media_id)
    except services.ProviderAPIError as exc:
        raise HttpError(502, f"{source} metadata unavailable") from exc


class AnimeEpisodeIn(Schema):
    """Mark episode N watched (progress = N) or unwatched (progress = N - 1)."""

    episode: int
    watched: bool = True


@router.post("/anime/{source}/{media_id}/episodes")
def mark_episode(request, source: str, media_id: str, payload: AnimeEpisodeIn):
    """Set anime progress from an episode, tracking the anime if needed."""
    require_write(request)
    _check(source)
    if payload.episode < 1:
        raise HttpError(400, "Episodes start at 1")
    entry = _entry(request.user, source, media_id)
    if entry is None:
        if not payload.watched:
            return _payload(request.user, source, media_id)
        meta = services.get_media_metadata(MediaTypes.ANIME.value, media_id, source)
        item, _ = Item.objects.get_or_create(
            media_id=media_id,
            source=source,
            media_type=MediaTypes.ANIME.value,
            defaults={"title": meta["title"], "image": meta["image"]},
        )
        entry = Anime(
            item=item,
            user=request.user,
            status=Status.IN_PROGRESS.value,
            start_date=timezone.now().replace(second=0, microsecond=0),
        )

    target = payload.episode if payload.watched else payload.episode - 1
    if (
        entry.status in (Status.COMPLETED.value, Status.PLANNING.value)
        and target != entry.progress
    ):
        # Moving progress re-opens a finished or planned anime; Media.save
        # completes it again when the last episode is reached.
        entry.status = Status.IN_PROGRESS.value
    if entry.start_date is None and target > 0:
        entry.start_date = timezone.now().replace(second=0, microsecond=0)
    entry.progress = target
    entry._history_user = request.user
    entry.save()
    return _payload(request.user, source, media_id)
