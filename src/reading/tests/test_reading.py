import datetime
import hashlib
import json
from unittest.mock import patch

from django.contrib.auth import get_user_model
from django.test import TestCase
from django.utils import timezone

from api.models import ApiToken, TokenScope
from app.models import Book, Item, MediaTypes, Sources, Status
from reading import services
from reading.models import PageEvent, ReadingDocument, ReadingSession

METADATA = {"title": "Dune", "image": "https://img/dune.jpg", "max_progress": 400}


def _epoch(dt):
    return int(dt.timestamp())


def _page_stats(start, pages, seconds=60, gap_after=None):
    """Consecutive page views starting at ``start``."""
    stats = []
    moment = start
    for page in pages:
        stats.append({"page": page, "start_time": _epoch(moment), "duration": seconds})
        moment += datetime.timedelta(seconds=seconds)
        if gap_after and page == gap_after[0]:
            moment += gap_after[1]
    return stats


@patch("app.models.Item.fetch_releases")
@patch("app.providers.services.get_media_metadata", return_value=METADATA)
class KOReaderIngestTests(TestCase):
    """Page events become sessions, and linked books get progress."""

    def setUp(self):
        """Create the user and tokens."""
        self.user = get_user_model().objects.create_user(username="reader")
        self.token, self.key = ApiToken.objects.create_token(
            self.user,
            "kindle",
            TokenScope.WRITE,
        )
        self.auth = {"HTTP_AUTHORIZATION": f"Bearer {self.key}"}
        self.start = timezone.now().replace(microsecond=0) - datetime.timedelta(hours=3)

    def _upload(self, page_stats, md5="abc123"):
        """Help: upload."""
        payload = {
            "device": "Kindle PW5",
            "books": [
                {
                    "md5": md5,
                    "title": "Dune",
                    "authors": "Frank Herbert",
                    "total_pages": 500,
                    "page_stats": page_stats,
                },
            ],
        }
        return self.client.post(
            "/api/v1/reading/koreader",
            json.dumps(payload),
            content_type="application/json",
            **self.auth,
        )

    def test_gap_splits_sessions(self, *_):
        """Check gap splits sessions."""
        stats = _page_stats(
            self.start,
            range(1, 11),
            gap_after=(5, datetime.timedelta(minutes=30)),
        )
        response = self._upload(stats)
        self.assertEqual(response.status_code, 200)
        sessions = ReadingSession.objects.order_by("started_at")
        self.assertEqual(
            [(s.start_page, s.end_page, s.pages_read) for s in sessions],
            [(1, 5, 5), (6, 10, 5)],
        )
        self.assertEqual(sessions[0].duration_seconds, 300)

    def test_resend_is_idempotent(self, *_):
        """Check resend is idempotent."""
        stats = _page_stats(self.start, range(1, 6))
        self._upload(stats)
        response = self._upload(stats)
        self.assertEqual(response.json()["books"][0]["new_page_events"], 0)
        self.assertEqual(PageEvent.objects.count(), 5)
        self.assertEqual(ReadingSession.objects.count(), 1)

    def test_short_page_views_ignored(self, *_):
        """Check short page views ignored."""
        stats = _page_stats(self.start, range(1, 4), seconds=2)
        self._upload(stats)
        self.assertEqual(PageEvent.objects.count(), 0)

    def test_read_only_token_cannot_upload(self, *_):
        """Check read only token cannot upload."""
        _, key = ApiToken.objects.create_token(self.user, "assistant")
        response = self.client.post(
            "/api/v1/reading/koreader",
            json.dumps({"books": []}),
            content_type="application/json",
            HTTP_AUTHORIZATION=f"Bearer {key}",
        )
        self.assertEqual(response.status_code, 403)

    def test_link_then_kosync_updates_book(self, *_):
        """Check link then kosync updates book."""
        self._upload(_page_stats(self.start, range(1, 6)))
        document = ReadingDocument.objects.get()
        response = self.client.post(
            f"/api/v1/reading/documents/{document.id}/link",
            json.dumps({"source": Sources.HARDCOVER.value, "media_id": "42"}),
            content_type="application/json",
            **self.auth,
        )
        self.assertEqual(response.status_code, 200)
        self.assertEqual(ReadingSession.objects.get().item.media_id, "42")

        kosync_headers = {
            "HTTP_X_AUTH_USER": "reader",
            "HTTP_X_AUTH_KEY": hashlib.md5(self.key.encode()).hexdigest(),  # noqa: S324
        }
        response = self.client.put(
            "/kosync/syncs/progress",
            json.dumps(
                {
                    "document": "abc123",
                    "progress": "/body/DocFragment[12]",
                    "percentage": 0.25,
                    "device": "Kindle",
                    "device_id": "K1",
                },
            ),
            content_type="application/json",
            **kosync_headers,
        )
        self.assertEqual(response.status_code, 200)
        book = Book.objects.get(user=self.user)
        self.assertEqual(book.status, Status.IN_PROGRESS.value)
        self.assertEqual(book.progress, 100)  # 25% of the edition's 400 pages

        response = self.client.get("/kosync/syncs/progress/abc123", **kosync_headers)
        self.assertEqual(response.json()["progress"], "/body/DocFragment[12]")

    def test_stats_and_streak(self, *_):
        """Check stats and streak."""
        today = timezone.localdate()
        for days_ago in (0, 1, 2, 5):
            day = today - datetime.timedelta(days=days_ago)
            start = timezone.make_aware(
                datetime.datetime.combine(day, datetime.time(20, 0)),
            )
            self._upload(_page_stats(start, range(1, 4)), md5=f"book{days_ago}")
        week_ago = today - datetime.timedelta(days=6)
        response = self.client.get(
            f"/api/v1/reading/stats?start={week_ago}", **self.auth
        )
        data = response.json()
        self.assertTrue(data["read_today"])
        self.assertEqual(data["current_streak"], 3)
        self.assertEqual(data["totals"]["pages"], 12)
        self.assertEqual(data["totals"]["days_read"], 4)
        self.assertEqual(len(data["days"]), 7)


class KOSyncAuthTests(TestCase):
    """The KOSync login flow."""

    def setUp(self):
        """Create the user and tokens."""
        self.user = get_user_model().objects.create_user(username="reader")
        _, self.key = ApiToken.objects.create_token(
            self.user, "kindle", TokenScope.WRITE
        )

    def _headers(self, key):
        """Help: headers."""
        return {
            "HTTP_X_AUTH_USER": "reader",
            "HTTP_X_AUTH_KEY": hashlib.md5(key.encode()).hexdigest(),  # noqa: S324
        }

    def test_auth_ok_and_bad(self):
        """Check auth ok and bad."""
        self.assertEqual(
            self.client.get(
                "/kosync/users/auth", **self._headers(self.key)
            ).status_code,
            200,
        )
        self.assertEqual(
            self.client.get("/kosync/users/auth", **self._headers("nope")).status_code,
            401,
        )

    def test_revoked_token_rejected(self):
        """Check revoked token rejected."""
        ApiToken.objects.update(revoked_at=timezone.now())
        response = self.client.get("/kosync/users/auth", **self._headers(self.key))
        self.assertEqual(response.status_code, 401)

    def test_registration_refused(self):
        """Check registration refused."""
        response = self.client.post("/kosync/users/create", "{}", "application/json")
        self.assertEqual(response.status_code, 402)


class GroupingTests(TestCase):
    """Session grouping on its own."""

    def test_empty(self):
        """Check empty."""
        self.assertEqual(services.group_events([]), [])


@patch("app.models.Item.fetch_releases")
@patch("app.providers.services.get_media_metadata", return_value=METADATA)
class CompletedBookTests(TestCase):
    """Re-opening a finished book doesn't regress it."""

    def test_completed_book_untouched(self, *_):
        """Check completed book untouched."""
        user = get_user_model().objects.create_user(username="reader")
        item = Item.objects.create(
            media_id="42",
            source=Sources.HARDCOVER.value,
            media_type=MediaTypes.BOOK.value,
            title="Dune",
            image="https://img/dune.jpg",
        )
        Book.objects.create(user=user, item=item, status=Status.COMPLETED.value)
        document = services.get_or_create_document(user, "abc")
        document.item = item
        document.percentage = 0.1
        document.save()
        book = services.sync_library_progress(document)
        self.assertEqual(book.status, Status.COMPLETED.value)
        self.assertEqual(book.progress, 400)
