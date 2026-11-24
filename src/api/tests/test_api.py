import json
from unittest.mock import patch

from django.contrib.auth import get_user_model
from django.test import TestCase

from api.models import ApiToken, TokenScope
from app.models import Anime, Item, MediaTypes, Sources, Status

METADATA = {
    "title": "Frieren",
    "image": "https://img/frieren.jpg",
    "max_progress": 28,
    "synopsis": "An elf mage outlives her party.",
}


class TokenAuthTests(TestCase):
    """Bearer tokens, scopes and token management."""

    def setUp(self):
        """Create the user and tokens."""
        self.user = get_user_model().objects.create_user(
            username="viewer",
            password="pw-12345678",  # noqa: S106 test fixture
        )
        _, self.read_key = ApiToken.objects.create_token(self.user, "assistant")

    def test_missing_and_bad_token(self):
        """Check missing and bad token."""
        self.assertEqual(self.client.get("/api/v1/me").status_code, 401)
        response = self.client.get("/api/v1/me", HTTP_AUTHORIZATION="Bearer nope")
        self.assertEqual(response.status_code, 401)

    def test_me(self):
        """Check me."""
        response = self.client.get(
            "/api/v1/me",
            HTTP_AUTHORIZATION=f"Bearer {self.read_key}",
        )
        self.assertEqual(response.json()["scope"], "read")

    def test_tokens_require_session(self):
        """Check tokens require session."""
        response = self.client.get(
            "/api/v1/tokens",
            HTTP_AUTHORIZATION=f"Bearer {self.read_key}",
        )
        self.assertEqual(response.status_code, 403)

    def test_session_can_create_token(self):
        """Check session can create token."""
        self.client.force_login(self.user)
        csrf = self.client.get("/api/v1/me")  # sets no cookie; ninja checks CSRF
        self.assertEqual(csrf.status_code, 200)
        self.client.handler.enforce_csrf_checks = False
        response = self.client.post(
            "/api/v1/tokens",
            json.dumps({"name": "kindle", "scope": "write"}),
            content_type="application/json",
        )
        self.assertEqual(response.status_code, 200, response.content)
        key = response.json()["key"]
        self.assertTrue(ApiToken.objects.authenticate(key).can_write)

    def test_raw_key_not_stored(self):
        """Check raw key not stored."""
        self.assertFalse(ApiToken.objects.filter(key_hash=self.read_key).exists())


@patch("app.models.Item.fetch_releases")
@patch("app.providers.services.get_media_metadata", return_value=METADATA)
class LibraryTests(TestCase):
    """Library read/write and the assistant summary."""

    def setUp(self):
        """Create the user and tokens."""
        self.user = get_user_model().objects.create_user(username="watcher")
        _, write_key = ApiToken.objects.create_token(
            self.user,
            "app",
            TokenScope.WRITE,
        )
        _, read_key = ApiToken.objects.create_token(self.user, "assistant")
        self.write = {"HTTP_AUTHORIZATION": f"Bearer {write_key}"}
        self.read = {"HTTP_AUTHORIZATION": f"Bearer {read_key}"}

    def _track(self, **extra):
        """Help: track."""
        body = {
            "media_type": MediaTypes.ANIME.value,
            "source": Sources.MAL.value,
            "media_id": "52991",
            "status": Status.IN_PROGRESS.value,
            "progress": 3,
            **extra,
        }
        return self.client.post(
            "/api/v1/library",
            json.dumps(body),
            content_type="application/json",
            **self.write,
        )

    def test_track_update_and_read(self, *_):
        """Check track update and read."""
        response = self._track()
        self.assertEqual(response.status_code, 200, response.content)
        entry_id = response.json()["id"]

        response = self._track(instance_id=entry_id, progress=5, notes="Fern is great")
        self.assertEqual(response.json()["progress"], 5)
        anime = Anime.objects.get()
        self.assertEqual(anime.notes, "Fern is great")
        self.assertEqual(anime.status, Status.IN_PROGRESS.value)

        response = self.client.get(f"/api/v1/library/anime/{entry_id}", **self.read)
        data = response.json()
        self.assertEqual(data["metadata"]["synopsis"], METADATA["synopsis"])
        self.assertGreaterEqual(len(data["history"]), 2)

        listing = self.client.get("/api/v1/library?media_type=anime", **self.read)
        self.assertEqual(listing.json()["count"], 1)

    def test_read_only_cannot_write(self, *_):
        """Check read only cannot write."""
        response = self.client.post(
            "/api/v1/library",
            json.dumps(
                {"media_type": "anime", "source": "mal", "media_id": "1"},
            ),
            content_type="application/json",
            **self.read,
        )
        self.assertEqual(response.status_code, 403)

    def test_other_users_entries_hidden(self, *_):
        """Check other users entries hidden."""
        other = get_user_model().objects.create_user(username="other")
        item = Item.objects.create(
            media_id="1",
            source=Sources.MAL.value,
            media_type=MediaTypes.ANIME.value,
            title="X",
            image="https://img/x.jpg",
        )
        entry = Anime.objects.create(user=other, item=item)
        response = self.client.get(f"/api/v1/library/anime/{entry.id}", **self.read)
        self.assertEqual(response.status_code, 404)

    def test_summary_and_activity(self, *_):
        """Check summary and activity."""
        self._track()
        summary = self.client.get("/api/v1/summary", **self.read).json()
        self.assertEqual(summary["in_progress"]["anime"][0]["item"]["title"], "Frieren")
        self.assertFalse(summary["reading"]["read_today"])

        activity = self.client.get("/api/v1/activity", **self.read)
        self.assertEqual(activity.status_code, 200, activity.content)
        self.assertGreaterEqual(len(activity.json()["entries"]), 1)

    def test_openapi_schema(self, *_):
        """Check openapi schema."""
        response = self.client.get("/api/v1/openapi.json")
        self.assertEqual(response.status_code, 200)
        self.assertIn("/api/v1/summary", response.json()["paths"])
