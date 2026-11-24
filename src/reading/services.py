"""Reading ingest: turn device page events into sessions and library progress."""

import datetime
import itertools
import logging
from collections import defaultdict

from django.db import transaction
from django.db.models import Count, Q, Sum
from django.db.models.functions import TruncDate
from django.utils import timezone

from app.models import Book, MediaTypes, Status
from app.providers import services as provider_services
from reading.models import PageEvent, ReadingDocument, ReadingSession, SessionSource

logger = logging.getLogger(__name__)

# A pause longer than this between page turns starts a new session.
SESSION_GAP = datetime.timedelta(minutes=10)
# KOReader ignores page views shorter than ~5s by default; so do we.
MIN_PAGE_SECONDS = 5
# KOReader rarely reports exactly 1.0 on the last page.
COMPLETE_PERCENTAGE = 0.99


def _from_epoch(value):
    return datetime.datetime.fromtimestamp(int(value), tz=datetime.UTC)


def get_or_create_document(user, document_hash, **fields):
    """Return the user's document for a hash, filling in any new metadata."""
    document, created = ReadingDocument.objects.get_or_create(
        user=user,
        document_hash=document_hash.lower(),
        defaults={k: v for k, v in fields.items() if v not in (None, "")},
    )
    if not created:
        changed = []
        for key, value in fields.items():
            if value not in (None, "") and getattr(document, key) != value:
                setattr(document, key, value)
                changed.append(key)
        if changed:
            document.save(update_fields=[*changed, "updated_at"])
    return document


def ingest_koreader_book(user, book, device=""):
    """Store one book's page events from a KOReader statistics upload.

    ``book`` is a dict with ``md5``, ``title``, ``authors``, ``total_pages`` and
    ``page_stats`` (``page``, ``start_time`` epoch seconds, ``duration`` seconds,
    optional ``total_pages``). Returns ``(document, new_event_count)``.
    """
    document = get_or_create_document(
        user,
        book["md5"],
        title=book.get("title") or "",
        authors=book.get("authors") or "",
        total_pages=book.get("total_pages"),
    )

    events = [
        PageEvent(
            document=document,
            page=int(stat["page"]),
            start_time=_from_epoch(stat["start_time"]),
            duration_seconds=int(stat["duration"]),
            total_pages=stat.get("total_pages") or book.get("total_pages"),
        )
        for stat in book.get("page_stats", [])
        if int(stat.get("duration", 0)) >= MIN_PAGE_SECONDS
    ]
    before = document.page_events.count()
    PageEvent.objects.bulk_create(events, ignore_conflicts=True, batch_size=500)
    added = document.page_events.count() - before

    if added:
        rebuild_sessions(document, device=device)
    return document, added


def group_events(events):
    """Split time-ordered page events into sessions separated by ``SESSION_GAP``."""
    groups = []
    current = []
    current_end = None
    for event in events:
        if current and event.start_time - current_end > SESSION_GAP:
            groups.append(current)
            current = []
        current.append(event)
        end = event.start_time + datetime.timedelta(seconds=event.duration_seconds)
        current_end = max(current_end, end) if current_end else end
    if current:
        groups.append(current)
    return groups


@transaction.atomic
def rebuild_sessions(document, device=""):
    """Recreate a document's KOReader sessions from its stored page events."""
    ReadingSession.objects.filter(
        document=document,
        source=SessionSource.KOREADER,
    ).delete()

    sessions = []
    for group in group_events(document.page_events.order_by("start_time")):
        pages = [event.page for event in group]
        last = group[-1]
        sessions.append(
            ReadingSession(
                user=document.user,
                source=SessionSource.KOREADER,
                document=document,
                item=document.item,
                title=document.title,
                started_at=group[0].start_time,
                ended_at=last.start_time
                + datetime.timedelta(seconds=last.duration_seconds),
                duration_seconds=sum(event.duration_seconds for event in group),
                pages_read=len(set(pages)),
                start_page=pages[0],
                end_page=pages[-1],
                device=device or document.device,
            ),
        )
    ReadingSession.objects.bulk_create(sessions)
    return sessions


def update_document_progress(document, *, progress, percentage, device, device_id):
    """Record a KOSync position and push it to the linked library entry."""
    document.progress = progress or ""
    document.percentage = percentage
    document.device = device or ""
    document.device_id = device_id or ""
    document.progress_updated_at = timezone.now()
    document.save()
    sync_library_progress(document)


def link_document(document, item):
    """Link a device document to a library item and backfill its sessions."""
    document.item = item
    document.save(update_fields=["item", "updated_at"])
    document.sessions.update(item=item)
    sync_library_progress(document)


def sync_library_progress(document):
    """Mirror a linked document's position onto the user's Book entry.

    Progress is stored in the edition's page count (from Hardcover or Open
    Library) so it lines up with the rest of the library; KOReader's own page
    count depends on font size, so the percentage is what gets carried over.
    Finished entries are left alone so a re-open doesn't regress them.
    """
    item = document.item
    if item is None or item.media_type != MediaTypes.BOOK.value:
        return None
    if document.percentage is None:
        return None

    user = document.user
    book = Book.objects.filter(user=user, item=item).order_by("-created_at").first()
    if book and book.status == Status.COMPLETED.value:
        return book

    try:
        max_progress = provider_services.get_media_metadata(
            item.media_type,
            item.media_id,
            item.source,
        ).get("max_progress")
    except provider_services.ProviderAPIError:
        logger.warning("Metadata unavailable for %s; using device pages", item)
        max_progress = None
    total = max_progress or document.total_pages
    if not total:
        return book

    if document.percentage >= COMPLETE_PERCENTAGE:
        progress = total
    else:
        progress = round(document.percentage * total)

    first_session = document.sessions.order_by("started_at").first()
    if book is None:
        book = Book(
            user=user,
            item=item,
            status=Status.IN_PROGRESS.value,
            start_date=first_session.started_at if first_session else timezone.now(),
        )
    elif book.status in (Status.PLANNING.value, Status.PAUSED.value):
        book.status = Status.IN_PROGRESS.value
        book.start_date = book.start_date or (
            first_session.started_at if first_session else timezone.now()
        )

    if progress != book.progress or book.pk is None:
        book.progress = progress
        book._history_user = user  # attribute read by simple_history
        book.save()
    return book


def daily_stats(user, start_date, end_date, tz=None):
    """Return per-day reading totals between two dates (inclusive).

    Days without reading are included with zeros so callers can spot gaps.
    """
    tz = tz or timezone.get_current_timezone()
    rows = (
        ReadingSession.objects.filter(
            user=user,
            started_at__date__gte=start_date,
            started_at__date__lte=end_date,
        )
        .annotate(day=TruncDate("started_at", tzinfo=tz))
        .values("day")
        .annotate(
            pages=Sum("pages_read"),
            chapters=Sum("chapters_read"),
            seconds=Sum("duration_seconds"),
            sessions=Count("id"),
        )
    )
    by_day = {row["day"]: row for row in rows}
    days = []
    day = start_date
    while day <= end_date:
        row = by_day.get(day, {})
        days.append(
            {
                "date": day,
                "pages": row.get("pages") or 0,
                "chapters": row.get("chapters") or 0,
                "minutes": round((row.get("seconds") or 0) / 60),
                "sessions": row.get("sessions") or 0,
            },
        )
        day += datetime.timedelta(days=1)
    return days


def streaks(user, today=None, tz=None):
    """Return ``(current, longest)`` consecutive reading-day streaks.

    The current streak counts today if the user has read today, otherwise it
    runs up to yesterday (a streak isn't broken until the day is over).
    """
    tz = tz or timezone.get_current_timezone()
    today = today or timezone.localdate()
    read_days = sorted(
        set(
            ReadingSession.objects.filter(
                Q(pages_read__gt=0) | Q(chapters_read__gt=0),
                user=user,
            )
            .annotate(day=TruncDate("started_at", tzinfo=tz))
            .values_list("day", flat=True),
        ),
    )
    if not read_days:
        return 0, 0

    longest = run = 1
    for previous, day in itertools.pairwise(read_days):
        run = run + 1 if (day - previous).days == 1 else 1
        longest = max(longest, run)

    current = 0
    day = today if read_days[-1] == today else today - datetime.timedelta(days=1)
    read_set = set(read_days)
    while day in read_set:
        current += 1
        day -= datetime.timedelta(days=1)
    return current, longest


def books_summary(user):
    """Return per-document reading totals: pages, time, sessions, position."""
    totals = defaultdict(dict)
    for row in (
        ReadingSession.objects.filter(user=user, document__isnull=False)
        .values("document_id")
        .annotate(
            pages=Sum("pages_read"),
            seconds=Sum("duration_seconds"),
            sessions=Count("id"),
        )
    ):
        totals[row["document_id"]] = row
    result = []
    for document in ReadingDocument.objects.filter(user=user).select_related("item"):
        row = totals.get(document.id, {})
        result.append(
            {
                "document": document,
                "pages_read": row.get("pages") or 0,
                "minutes": round((row.get("seconds") or 0) / 60),
                "sessions": row.get("sessions") or 0,
            },
        )
    return result
