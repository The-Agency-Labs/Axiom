"""Year filters and the year-end snapshot ("Year in review")."""

import datetime
from collections import Counter, defaultdict

from django.apps import apps
from django.core.cache import cache
from django.utils import timezone
from ninja import Router

from api.serializers import item_dict, jsonable
from app.models import Episode, MediaTypes, Status
from app.providers import services
from reading import services as reading_services
from reading.models import ReadingSession

router = Router(tags=["years"])

# Types whose progress is a plain integer field with history.
COUNTED = [
    MediaTypes.ANIME.value,
    MediaTypes.MANGA.value,
    MediaTypes.BOOK.value,
    MediaTypes.COMIC.value,
    MediaTypes.GAME.value,
    MediaTypes.BOARDGAME.value,
]
ENTRY_TYPES = [MediaTypes.TV.value, MediaTypes.MOVIE.value, *COUNTED]
GENRE_LOOKUPS = 40
SNAPSHOT_TTL = 10 * 60


def _model(media_type):
    return apps.get_model(app_label="app", model_name=media_type)


def _as_date(value):
    if value is None:
        return None
    if isinstance(value, datetime.datetime):
        return (
            timezone.localtime(value).date()
            if timezone.is_aware(value)
            else value.date()
        )
    return value


def active_in_year(entry, year):
    """Return whether an entry saw any activity during ``year``.

    An entry counts for a year if it was started, progressed or finished in
    it, or was ongoing across it (started before and finished after).
    """
    start = _as_date(entry.start_date) or _as_date(entry.created_at)
    end = (
        _as_date(entry.end_date)
        or _as_date(getattr(entry, "progressed_at", None))
        or (timezone.localdate() if entry.status == Status.IN_PROGRESS.value else start)
    )
    if start is None:
        return False
    first, last = datetime.date(year, 1, 1), datetime.date(year, 12, 31)
    return start <= last and (end or start) >= first


def entry_years(entry):
    """Every year an entry was active in."""
    start = _as_date(entry.start_date) or _as_date(entry.created_at)
    if start is None:
        return set()
    end = (
        _as_date(entry.end_date)
        or _as_date(getattr(entry, "progressed_at", None))
        or (timezone.localdate() if entry.status == Status.IN_PROGRESS.value else start)
    )
    end = max(end or start, start)
    return set(range(start.year, end.year + 1))


@router.get("/years")
def years(request):
    """Years with any tracked activity, newest first."""
    found = set()
    for kind in ENTRY_TYPES:
        for entry in (
            _model(kind).objects.filter(user=request.user).select_related("item")
        ):
            found |= entry_years(entry)
    found |= {
        d.year
        for d in ReadingSession.objects.filter(user=request.user).dates(
            "started_at", "year"
        )
    }
    return sorted(found, reverse=True)


def _progress_deltas(entry, year):
    """Units of progress gained during ``year``, by month, from the change log."""
    by_month = Counter()
    previous = 0
    for record in entry.history.order_by("history_date").only(
        "history_date", "progress"
    ):
        progress = record.progress or 0
        gained = progress - previous
        previous = progress
        when = _as_date(record.history_date)
        if gained > 0 and when.year == year:
            by_month[when.month] += gained
    return by_month


def _genres(media_type, item):
    try:
        meta = services.get_media_metadata(media_type, item.media_id, item.source)
    except Exception:  # noqa: BLE001 genres are best effort
        return []
    return [
        g if isinstance(g, str) else g.get("name", "") for g in meta.get("genres") or []
    ]


def _finished_dict(entry, media_type):
    return {
        "id": entry.id,
        "media_type": media_type,
        "item": item_dict(entry.item),
        "score": jsonable(entry.score),
        "end_date": jsonable(entry.end_date),
    }


def _tv_section(user, year):
    episodes = list(
        Episode.objects.filter(
            related_season__user=user,
            end_date__year=year,
        ).select_related("item", "related_season__related_tv"),
    )
    by_month = Counter(_as_date(ep.end_date).month for ep in episodes)
    by_show = Counter()
    shows = {}
    seasons = defaultdict(set)
    for ep in episodes:
        tv = ep.related_season.related_tv
        by_show[tv.id] += 1
        shows[tv.id] = tv
        seasons[(ep.item.source, ep.item.media_id, ep.item.season_number)].add(
            ep.item.episode_number,
        )

    minutes = 0
    for (source, media_id, number), watched in seasons.items():
        try:
            meta = services.get_media_metadata(
                MediaTypes.SEASON.value,
                media_id,
                source,
                [number],
            )
        except Exception:  # noqa: BLE001, S112
            continue
        runtimes = {
            e["episode_number"]: e.get("runtime") or 0 for e in meta.get("episodes", [])
        }
        per_episode = [runtimes.get(n, 0) for n in watched]
        # Count rewatches too: scale by how many times episodes were logged.
        minutes += sum(per_episode)
    rewatch_extra = len(episodes) - sum(len(v) for v in seasons.values())
    if rewatch_extra > 0 and seasons:
        average = minutes / max(1, sum(len(v) for v in seasons.values()))
        minutes += round(average * rewatch_extra)

    top = [
        {
            "id": tv_id,
            "media_type": MediaTypes.TV.value,
            "item": item_dict(shows[tv_id].item),
            "count": count,
        }
        for tv_id, count in by_show.most_common(5)
    ]
    return {
        "units": len(episodes),
        "minutes": minutes,
        "titles": len(shows),
        "by_month": [by_month.get(m, 0) for m in range(1, 13)],
        "top": top,
    }


def _reading_section(user, year):
    today = timezone.localdate()
    first = datetime.date(year, 1, 1)
    last = min(datetime.date(year, 12, 31), today)
    if last < first:
        return None
    days = reading_services.daily_stats(user, first, last)
    read_days = [d for d in days if d["pages"] or d["chapters"]]
    longest = run = 0
    for d in days:
        run = run + 1 if (d["pages"] or d["chapters"]) else 0
        longest = max(longest, run)
    by_month = Counter()
    minutes_by_month = Counter()
    for d in days:
        month = datetime.date.fromisoformat(str(d["date"])).month
        by_month[month] += d["pages"]
        minutes_by_month[month] += d["minutes"]
    best = max(days, key=lambda d: d["pages"], default=None)
    return {
        "pages": sum(d["pages"] for d in days),
        "chapters": sum(d["chapters"] for d in days),
        "minutes": sum(d["minutes"] for d in days),
        "sessions": sum(d["sessions"] for d in days),
        "days_read": len(read_days),
        "days_in_range": len(days),
        "longest_streak": longest,
        "best_day": jsonable(best) if best and best["pages"] else None,
        "pages_by_month": [by_month.get(m, 0) for m in range(1, 13)],
        "minutes_by_month": [minutes_by_month.get(m, 0) for m in range(1, 13)],
        "days": jsonable(days),
    }


def build_snapshot(user, year):  # noqa: C901, PLR0912, PLR0915
    """Compute the year-end snapshot for one user."""
    sections = {}
    finished = []
    started = Counter()
    genre_items = []

    tv = _tv_section(user, year)
    for entry in (
        _model(MediaTypes.TV.value).objects.filter(user=user).select_related("item")
    ):
        end = _as_date(entry.end_date)
        if entry.status == Status.COMPLETED.value and end and end.year == year:
            finished.append(_finished_dict(entry, MediaTypes.TV.value))
        start = _as_date(entry.start_date)
        if start and start.year == year:
            started[MediaTypes.TV.value] += 1
        if active_in_year(entry, year):
            genre_items.append((MediaTypes.TV.value, entry))
    if tv["units"]:
        sections[MediaTypes.TV.value] = tv

    movies = []
    for entry in (
        _model(MediaTypes.MOVIE.value)
        .objects.filter(
            user=user,
            status=Status.COMPLETED.value,
            end_date__year=year,
        )
        .select_related("item")
    ):
        movies.append(entry)
        finished.append(_finished_dict(entry, MediaTypes.MOVIE.value))
        genre_items.append((MediaTypes.MOVIE.value, entry))
    if movies:
        by_month = Counter(_as_date(m.end_date).month for m in movies)
        sections[MediaTypes.MOVIE.value] = {
            "units": len(movies),
            "titles": len(movies),
            "by_month": [by_month.get(m, 0) for m in range(1, 13)],
            "top": [],
        }

    for kind in COUNTED:
        by_month = Counter()
        per_title = Counter()
        titles = {}
        for entry in _model(kind).objects.filter(user=user).select_related("item"):
            if not active_in_year(entry, year):
                continue
            deltas = _progress_deltas(entry, year)
            gained = sum(deltas.values())
            by_month.update(deltas)
            end = _as_date(entry.end_date)
            done = entry.status == Status.COMPLETED.value and end and end.year == year
            if done:
                finished.append(_finished_dict(entry, kind))
            start = _as_date(entry.start_date)
            if start and start.year == year:
                started[kind] += 1
            if gained or done:
                genre_items.append((kind, entry))
            if gained:
                per_title[entry.id] = gained
                titles[entry.id] = entry
        if per_title or any(f["media_type"] == kind for f in finished):
            sections[kind] = {
                "units": sum(by_month.values()),
                "titles": len(per_title),
                "by_month": [by_month.get(m, 0) for m in range(1, 13)],
                "top": [
                    {
                        "id": entry_id,
                        "media_type": kind,
                        "item": item_dict(titles[entry_id].item),
                        "count": count,
                    }
                    for entry_id, count in per_title.most_common(5)
                ],
            }

    for kind, section in sections.items():
        section["finished"] = sum(1 for f in finished if f["media_type"] == kind)
        section["started"] = started.get(kind, 0)

    finished.sort(key=lambda f: f["end_date"] or "")
    rated = sorted(
        (f for f in finished if f["score"] is not None),
        key=lambda f: f["score"],
        reverse=True,
    )

    genres = Counter()
    for kind, entry in genre_items[:GENRE_LOOKUPS]:
        genres.update({g for g in _genres(kind, entry.item) if g})

    return {
        "year": year,
        "generated_at": jsonable(timezone.now()),
        "totals": {
            "finished": len(finished),
            "titles": sum(s["titles"] for s in sections.values()),
            "watch_minutes": tv["minutes"],
        },
        "sections": sections,
        "reading": _reading_section(user, year),
        "finished": finished,
        "top_rated": rated[:8],
        "genres": [{"name": n, "count": c} for n, c in genres.most_common(10)],
    }


@router.get("/years/{year}")
def snapshot(request, year: int, refresh: bool = False):  # noqa: FBT001, FBT002 query parameter
    """Year-end snapshot: totals per type, monthly activity, finishes, reading."""
    key = f"encompass:year:{request.user.id}:{year}"
    data = None if refresh else cache.get(key)
    if data is None:
        data = build_snapshot(request.user, year)
        cache.set(key, data, SNAPSHOT_TTL)
    return data
