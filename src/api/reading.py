"""Reading: KOReader stats upload, sessions, daily stats and streaks."""

import datetime

from django.shortcuts import get_object_or_404
from django.utils import timezone
from ninja import Router, Schema
from ninja.errors import HttpError

from api.auth import require_write
from api.serializers import document_dict, jsonable, session_dict
from app.models import Item, MediaTypes
from app.providers import services as provider_services
from reading import services
from reading.models import ReadingDocument, ReadingSession, SessionSource

router = Router(tags=["reading"])

MAX_RANGE_DAYS = 3660


def _date_range(start, end, default_days=30):
    end = end or timezone.localdate()
    start = start or end - datetime.timedelta(days=default_days - 1)
    if start > end:
        raise HttpError(400, "start must be before end")
    if (end - start).days > MAX_RANGE_DAYS:
        raise HttpError(400, "Range too large")
    return start, end


class PageStatIn(Schema):
    """One row of KOReader's ``page_stat_data`` table."""

    page: int
    start_time: int
    duration: int
    total_pages: int | None = None


class BookStatsIn(Schema):
    """One book from KOReader's ``book`` table plus its new page rows."""

    md5: str
    title: str = ""
    authors: str = ""
    total_pages: int | None = None
    page_stats: list[PageStatIn] = []


class KOReaderUploadIn(Schema):
    """Payload sent by the KOReader plugin on suspend, close or wake."""

    device: str = ""
    books: list[BookStatsIn]


@router.post("/reading/koreader")
def upload_koreader(request, payload: KOReaderUploadIn):
    """Ingest KOReader statistics; safe to resend the same rows."""
    require_write(request)
    results = []
    for book in payload.books:
        document, added = services.ingest_koreader_book(
            request.user,
            book.dict(),
            device=payload.device,
        )
        results.append(
            {
                "document_id": document.id,
                "md5": document.document_hash,
                "new_page_events": added,
                "linked": document.item_id is not None,
            },
        )
    return {"books": results}


@router.get("/reading/stats")
def reading_stats(
    request,
    start: datetime.date | None = None,
    end: datetime.date | None = None,
):
    """Per-day pages/minutes, streaks and totals. Defaults to the last 30 days."""
    start, end = _date_range(start, end)
    days = services.daily_stats(request.user, start, end)
    current, longest = services.streaks(request.user)
    today = timezone.localdate()
    today_row = next((d for d in days if d["date"] == today), None)
    active = [d for d in days if d["pages"] or d["chapters"]]
    return {
        "start": start,
        "end": end,
        "read_today": bool(today_row and (today_row["pages"] or today_row["chapters"])),
        "today": today_row,
        "current_streak": current,
        "longest_streak": longest,
        "totals": {
            "pages": sum(d["pages"] for d in days),
            "chapters": sum(d["chapters"] for d in days),
            "minutes": sum(d["minutes"] for d in days),
            "sessions": sum(d["sessions"] for d in days),
            "days_read": len(active),
            "days_in_range": len(days),
        },
        "days": jsonable(days),
    }


@router.get("/reading/sessions")
def list_sessions(
    request,
    start: datetime.date | None = None,
    end: datetime.date | None = None,
    limit: int = 100,
):
    """List reading sessions, newest first."""
    start, end = _date_range(start, end)
    sessions = ReadingSession.objects.filter(
        user=request.user,
        started_at__date__gte=start,
        started_at__date__lte=end,
    ).select_related("item")[: min(limit, 1000)]
    return [session_dict(s) for s in sessions]


class SessionIn(Schema):
    """A session reported by a client other than KOReader (e.g. Paperback)."""

    source: str = SessionSource.MANUAL
    title: str = ""
    item_id: int | None = None
    started_at: datetime.datetime
    ended_at: datetime.datetime
    pages_read: int = 0
    chapters_read: int = 0
    start_page: int | None = None
    end_page: int | None = None
    device: str = ""


@router.post("/reading/sessions")
def create_session(request, payload: SessionIn):
    """Log a reading session manually or from another app."""
    require_write(request)
    if payload.source not in SessionSource.values:
        raise HttpError(400, "Unknown source")
    if payload.ended_at < payload.started_at:
        raise HttpError(400, "ended_at must be after started_at")
    item = get_object_or_404(Item, id=payload.item_id) if payload.item_id else None
    session = ReadingSession.objects.create(
        user=request.user,
        item=item,
        title=payload.title or (item.title if item else ""),
        duration_seconds=int((payload.ended_at - payload.started_at).total_seconds()),
        **payload.dict(exclude={"item_id", "title"}),
    )
    return session_dict(session)


@router.get("/reading/documents")
def list_documents(request, unlinked: bool = False):  # noqa: FBT001, FBT002
    """Books KOReader has reported, with pages and time read on each."""
    rows = services.books_summary(request.user)
    if unlinked:
        rows = [r for r in rows if r["document"].item_id is None]
    return [
        {
            **document_dict(r["document"]),
            "pages_read": r["pages_read"],
            "minutes": r["minutes"],
            "sessions": r["sessions"],
        }
        for r in rows
    ]


@router.get("/reading/documents/{document_id}/suggestions")
def suggest_links(request, document_id: int, source: str | None = None):
    """Book search results to link an unmatched document to."""
    document = get_object_or_404(ReadingDocument, id=document_id, user=request.user)
    query = " ".join(filter(None, [document.title, document.authors.split("\n")[0]]))
    if not query:
        return []
    try:
        results = provider_services.search(MediaTypes.BOOK.value, query, 1, source)
    except provider_services.ProviderAPIError as exc:
        raise HttpError(502, "Search provider unavailable") from exc
    return jsonable(results.get("results", results))


class LinkIn(Schema):
    """Which library book a device document is."""

    source: str
    media_id: str


@router.post("/reading/documents/{document_id}/link")
def link_document(request, document_id: int, payload: LinkIn):
    """Link a document to a book; past sessions and progress follow."""
    require_write(request)
    document = get_object_or_404(ReadingDocument, id=document_id, user=request.user)
    try:
        meta = provider_services.get_media_metadata(
            MediaTypes.BOOK.value,
            payload.media_id,
            payload.source,
        )
    except provider_services.ProviderAPIError as exc:
        raise HttpError(502, "Metadata provider unavailable") from exc
    item, _ = Item.objects.get_or_create(
        media_id=payload.media_id,
        source=payload.source,
        media_type=MediaTypes.BOOK.value,
        defaults={"title": meta["title"], "image": meta["image"]},
    )
    services.link_document(document, item)
    return document_dict(document)
