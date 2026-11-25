"""TV shows: every season (specials included), episodes, and watch toggles.

Works for shows whether or not they are in the library, so a show can be
browsed from search before it is tracked. Marking an episode creates the
season (and show) entries on demand, like Yamtrack's own episode form.
"""

import datetime
import logging

from django.db.models import Prefetch
from django.utils import timezone
from ninja import Router, Schema
from ninja.errors import HttpError

from api.auth import require_write
from api.serializers import jsonable, media_dict
from app.models import TV, Episode, Item, MediaTypes, Season, Sources, Status
from app.providers import services

logger = logging.getLogger(__name__)
router = Router(tags=["shows"])

TMDB_STILL = "https://image.tmdb.org/t/p/w500"


def _tv_metadata(source, media_id):
    try:
        return services.get_media_metadata(MediaTypes.TV.value, media_id, source)
    except services.ProviderAPIError as exc:
        raise HttpError(502, f"{source} metadata unavailable") from exc


def _season_metadata(source, media_id, season_number):
    try:
        return services.get_media_metadata(
            MediaTypes.SEASON.value,
            media_id,
            source,
            [season_number],
        )
    except services.ProviderAPIError as exc:
        raise HttpError(502, f"{source} metadata unavailable") from exc


def _seasons_for(user, source, media_id):
    """Return the user's season entries for a show, keyed by season number."""
    queryset = (
        Season.objects.filter(
            user=user,
            item__source=source,
            item__media_id=media_id,
        )
        .select_related("item")
        .prefetch_related(
            Prefetch("episodes", queryset=Episode.objects.select_related("item")),
        )
    )
    return {season.item.season_number: season for season in queryset}


def _watched_numbers(season):
    return {ep.item.episode_number for ep in season.episodes.all()} if season else set()


def _aired(air_date, today):
    if not air_date:
        return False
    try:
        return datetime.date.fromisoformat(str(air_date)[:10]) <= today
    except ValueError:
        return False


@router.get("/shows/{source}/{media_id}")
def show(request, source: str, media_id: str):
    """Return a show with every season and the user's progress."""
    if source not in Sources.values:
        raise HttpError(400, "Unknown source")
    meta = _tv_metadata(source, media_id)
    tracked = _seasons_for(request.user, source, media_id)
    tv = (
        TV.objects.filter(
            user=request.user,
            item__source=source,
            item__media_id=media_id,
        )
        .select_related("item")
        .first()
    )

    seasons = []
    for related in meta.get("related", {}).get("seasons", []):
        number = related["season_number"]
        season = tracked.get(number)
        watched = len(_watched_numbers(season))
        total = related.get("max_progress") or 0
        seasons.append(
            {
                "season_number": number,
                "title": related.get("season_title") or f"Season {number}",
                "is_special": number == 0,
                "image": related.get("image"),
                "air_date": jsonable(related.get("first_air_date")),
                "episode_count": total,
                "watched": watched,
                "percent": round(watched / total * 100, 1) if total else None,
                "entry": (
                    {
                        "id": season.id,
                        "status": season.status,
                        "score": jsonable(season.score),
                    }
                    if season
                    else None
                ),
            },
        )
    # Regular seasons first, specials at the end.
    seasons.sort(key=lambda s: (s["is_special"], s["season_number"]))

    regular = [s for s in seasons if not s["is_special"]]
    watched = sum(s["watched"] for s in regular)
    total = sum(s["episode_count"] for s in regular)
    return {
        "source": source,
        "media_id": media_id,
        "title": meta["title"],
        "image": meta["image"],
        "entry": media_dict(tv, MediaTypes.TV.value) if tv else None,
        "completion": {
            "watched": watched,
            "total": total,
            "percent": round(watched / total * 100, 1) if total else None,
            "seasons_completed": sum(
                1
                for s in regular
                if s["episode_count"] and s["watched"] >= s["episode_count"]
            ),
            "seasons": len(regular),
            "specials_watched": sum(s["watched"] for s in seasons if s["is_special"]),
        },
        "status": meta.get("details", {}).get("status"),
        "seasons": seasons,
    }


def _episode_dict(raw, history, today):
    still = raw.get("still_path")
    image = raw.get("image") or (f"{TMDB_STILL}{still}" if still else None)
    return {
        "episode_number": raw["episode_number"],
        "title": raw.get("name")
        or raw.get("title")
        or f"Episode {raw['episode_number']}",
        "overview": raw.get("overview") or "",
        "air_date": raw.get("air_date"),
        "aired": _aired(raw.get("air_date"), today),
        "runtime": raw.get("runtime"),
        "image": image,
        "kind": raw.get("episode_type") or "standard",
        "score": raw.get("vote_average") or None,
        "watched": bool(history),
        "watch_count": len(history),
        "watched_at": jsonable(max((d for d in history if d), default=None)),
    }


def _season_payload(user, source, media_id, season_number):
    meta = _season_metadata(source, media_id, season_number)
    season = _seasons_for(user, source, media_id).get(season_number)
    history = {}
    if season:
        for ep in season.episodes.all():
            history.setdefault(ep.item.episode_number, []).append(ep.end_date)
    today = timezone.localdate()
    episodes = [
        _episode_dict(raw, history.get(raw["episode_number"], []), today)
        for raw in meta.get("episodes", [])
    ]
    watched = sum(1 for ep in episodes if ep["watched"])
    return {
        "source": source,
        "media_id": media_id,
        "season_number": season_number,
        "title": meta.get("season_title") or f"Season {season_number}",
        "image": meta.get("image"),
        "synopsis": meta.get("synopsis"),
        "entry": media_dict(season, MediaTypes.SEASON.value) if season else None,
        "watched": watched,
        "total": len(episodes),
        "runtime_minutes": sum(ep["runtime"] or 0 for ep in episodes),
        "episodes": episodes,
    }


@router.get("/shows/{source}/{media_id}/seasons/{season_number}")
def season(request, source: str, media_id: str, season_number: int):
    """Every episode of a season with air dates, runtimes and watch state."""
    if source not in Sources.values:
        raise HttpError(400, "Unknown source")
    return _season_payload(request.user, source, media_id, season_number)


class EpisodesIn(Schema):
    """Mark episodes watched or unwatched.

    ``episodes`` lists episode numbers; ``up_to`` marks 1..up_to instead.
    ``all`` marks the whole season. ``date`` defaults to now.
    """

    episodes: list[int] = []
    up_to: int | None = None
    all: bool = False
    watched: bool = True
    rewatch: bool = False
    date: datetime.datetime | None = None


def _get_or_create_season(user, source, media_id, season_number, meta):
    season = (
        Season.objects.filter(
            user=user,
            item__source=source,
            item__media_id=media_id,
            item__season_number=season_number,
        )
        .select_related("item")
        .first()
    )
    if season:
        return season
    item, _ = Item.objects.get_or_create(
        media_id=media_id,
        source=source,
        media_type=MediaTypes.SEASON.value,
        season_number=season_number,
        defaults={"title": meta["title"], "image": meta["image"]},
    )
    season = Season(
        item=item,
        user=user,
        score=None,
        status=Status.IN_PROGRESS.value,
        notes="",
    )
    season._history_user = user
    season.save()
    return season


@router.post("/shows/{source}/{media_id}/seasons/{season_number}/episodes")
def mark_episodes(  # noqa: C901
    request,
    source: str,
    media_id: str,
    season_number: int,
    payload: EpisodesIn,
):
    """Watch or unwatch episodes, creating the season entry when needed."""
    require_write(request)
    if source not in Sources.values:
        raise HttpError(400, "Unknown source")
    meta = _season_metadata(source, media_id, season_number)
    available = [ep["episode_number"] for ep in meta.get("episodes", [])]
    if payload.all:
        numbers = available
    elif payload.up_to is not None:
        numbers = [n for n in available if n <= payload.up_to]
    else:
        numbers = [n for n in payload.episodes if n in available]
    if not numbers:
        raise HttpError(400, "No matching episodes")

    if payload.watched:
        season = _get_or_create_season(
            request.user,
            source,
            media_id,
            season_number,
            meta,
        )
        already = _watched_numbers(season)
        when = payload.date or timezone.now().replace(second=0, microsecond=0)
        for number in sorted(numbers):
            if number in already and not payload.rewatch:
                continue
            season.watch(number, when)
    else:
        season = _seasons_for(request.user, source, media_id).get(season_number)
        if season:
            watched = _watched_numbers(season)
            for number in sorted(numbers, reverse=True):
                if number in watched:
                    season.unwatch(number)

    return _season_payload(request.user, source, media_id, season_number)
