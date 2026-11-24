"""Endpoints shaped for a personal assistant: a compact summary and an activity feed.

An assistant can call ``/summary`` for "what am I into right now", ``/activity``
for "what did I do last week", and ``/library/{type}/{id}`` (with metadata) for
quiz material such as synopsis, episodes watched and the user's own notes.
"""

import datetime

from django.apps import apps
from django.utils import timezone
from ninja import Router

from api.serializers import jsonable, media_dict, session_dict
from app import history_processor
from app.models import MediaTypes, Status
from reading import services as reading_services
from reading.models import ReadingSession

router = Router(tags=["assistant"])

SUMMARY_TYPES = [
    MediaTypes.TV.value,
    MediaTypes.MOVIE.value,
    MediaTypes.ANIME.value,
    MediaTypes.MANGA.value,
    MediaTypes.BOOK.value,
    MediaTypes.COMIC.value,
    MediaTypes.GAME.value,
]


def _aware(day, end=False):  # noqa: FBT002
    moment = datetime.datetime.combine(
        day,
        datetime.time.max if end else datetime.time.min,
    )
    return timezone.make_aware(moment)


@router.get("/me")
def me(request):
    """Who the credentials belong to and what they can do."""
    token = getattr(request, "api_token", None)
    return {
        "username": request.user.username,
        "auth": "token" if token else "session",
        "scope": token.scope if token else "write",
        "timezone": str(timezone.get_current_timezone()),
    }


@router.get("/summary")
def summary(request, recent_days: int = 30):
    """Everything in progress, recent completions and reading habits."""
    since = timezone.now() - datetime.timedelta(days=recent_days)
    in_progress = {}
    completed = []
    for media_type in SUMMARY_TYPES:
        model = apps.get_model(app_label="app", model_name=media_type)
        queryset = model.objects.filter(user=request.user).select_related("item")
        in_progress[media_type] = [
            media_dict(m, media_type)
            for m in queryset.filter(status=Status.IN_PROGRESS.value)
        ]
        finished = queryset.filter(status=Status.COMPLETED.value)
        if media_type == MediaTypes.TV.value:
            # A show's end date is derived from its seasons, not a column.
            recent = [m for m in finished if m.end_date and m.end_date >= since]
        else:
            recent = finished.filter(end_date__gte=since)
        completed.extend(media_dict(m, media_type) for m in recent)
    completed.sort(key=lambda e: e["end_date"] or "", reverse=True)

    today = timezone.localdate()
    week = reading_services.daily_stats(
        request.user,
        today - datetime.timedelta(days=6),
        today,
    )
    current, longest = reading_services.streaks(request.user)
    last_session = ReadingSession.objects.filter(user=request.user).first()
    return {
        "generated_at": timezone.now(),
        "in_progress": in_progress,
        "recently_completed": completed,
        "reading": {
            "read_today": bool(week[-1]["pages"] or week[-1]["chapters"]),
            "today": jsonable(week[-1]),
            "last_7_days": jsonable(week),
            "pages_last_7_days": sum(d["pages"] for d in week),
            "current_streak": current,
            "longest_streak": longest,
            "last_session": session_dict(last_session) if last_session else None,
        },
    }


@router.get("/activity")
def activity(
    request,
    start: datetime.date | None = None,
    end: datetime.date | None = None,
    limit: int = 50,
    cursor: str | None = None,
):
    """Tracking history, newest first (status changes, progress, scores, episodes).

    Pass the returned ``next_cursor`` to page further back.
    """
    limit = max(1, min(limit, 200))
    parsed_cursor = None
    if cursor:
        date_part, media_type, history_id = cursor.rsplit("|", 2)
        parsed_cursor = (
            datetime.datetime.fromisoformat(date_part),
            media_type,
            int(history_id),
        )
    rows, has_next = history_processor.get_journal_page(
        request.user,
        _aware(start) if start else None,
        _aware(end, end=True) if end else None,
        limit=limit,
        cursor=parsed_cursor,
    )
    entries = history_processor.build_journal_entries(rows, request.user)
    next_cursor = None
    if has_next and rows:
        last_date, last_type, last_id = rows[-1]
        next_cursor = f"{last_date.isoformat()}|{last_type}|{last_id}"
    return {
        "entries": [
            {
                "date": jsonable(e["date"]),
                "media_type": e["media_type"],
                "item": jsonable(e["item"]),
                "changes": jsonable(e["changes"]),
            }
            for e in entries
        ],
        "next_cursor": next_cursor,
    }
