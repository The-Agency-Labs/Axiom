import datetime
from collections import Counter
from unittest.mock import patch

from django.contrib.auth import get_user_model
from django.test import TestCase
from django.utils import timezone

from app.models import Item, MediaTypes, Movie, Sources, Status
from integrations.imports import jellyfin
from integrations.webhooks.jellyfin import JellyfinWebhookProcessor

USERS = [{"Name": "Shyan", "Id": "u1"}]
SERIES = {
    "Items": [{"Id": "s1", "ProviderIds": {"Tmdb": "1668"}}],
    "TotalRecordCount": 1,
}


def played(**fields):
    """Build a played Jellyfin item."""
    item = {"UserData": {"Played": True, "LastPlayedDate": "2026-01-02T03:04:05Z"}}
    item.update(fields)
    return item


class FakeClient(jellyfin.JellyfinClient):
    """Serves canned API responses."""

    def __init__(self, items):  # noqa: D107
        self.items_page = {"Items": items, "TotalRecordCount": len(items)}

    def get(self, path, **params):  # noqa: D102
        if path == "/Users":
            return USERS
        if params.get("IncludeItemTypes") == "Series":
            return SERIES
        return self.items_page


class JellyfinImportTests(TestCase):
    """The importer replays played items through the webhook processor."""

    def setUp(self):
        """Create the Encompass user."""
        self.user = get_user_model().objects.create_user(username="shyan")

    @patch.object(JellyfinWebhookProcessor, "process_payload", autospec=True)
    def test_replays_played_items_oldest_first(self, mock_process):
        """Each played item becomes a Stop payload with its own watch date."""
        items = [
            played(
                Type="Episode",
                SeriesId="s1",
                SeriesName="Friends",
                ParentIndexNumber=1,
                IndexNumber=2,
                ProviderIds={"Tvdb": "2"},
                UserData={"Played": True, "LastPlayedDate": "2026-03-01T00:00:00Z"},
            ),
            played(
                Type="Movie",
                Name="Dune",
                ProductionYear=2021,
                ProviderIds={"Tmdb": "438631"},
            ),
        ]
        counts, failures, latest = jellyfin.import_history(
            self.user, FakeClient(items), "shyan"
        )

        self.assertEqual(counts, Counter({"Movie": 1, "Episode": 1}))
        self.assertEqual(failures, [])
        self.assertEqual(latest, datetime.datetime(2026, 3, 1, tzinfo=datetime.UTC))
        first, second = (call.args for call in mock_process.call_args_list)
        self.assertEqual(first[1]["Item"]["Type"], "Movie")  # older play first
        processor, payload, _user = second
        self.assertTrue(processor.import_mode)
        self.assertEqual(processor.watched_at.month, 3)
        self.assertEqual(payload["Event"], "Stop")
        self.assertTrue(payload["Item"]["UserData"]["Played"])
        self.assertEqual(payload["Series"]["ProviderIds"], {"Tmdb": "1668"})

    @patch.object(JellyfinWebhookProcessor, "process_payload", autospec=True)
    def test_since_skips_older_plays(self, mock_process):
        """Catch-up runs only replay plays after the last sync."""
        items = [played(Type="Movie", Name="Old", ProviderIds={"Tmdb": "1"})]
        since = datetime.datetime(2026, 6, 1, tzinfo=datetime.UTC)
        counts, _, _ = jellyfin.import_history(
            self.user, FakeClient(items), "shyan", since=since
        )
        self.assertEqual(counts, Counter())
        mock_process.assert_not_called()

    @patch.object(JellyfinWebhookProcessor, "process_payload", autospec=True)
    def test_one_failure_does_not_stop_the_import(self, mock_process):
        """A failing item is reported and the rest continue."""
        mock_process.side_effect = [ValueError("boom"), None]
        items = [
            played(Type="Movie", Name="A", ProviderIds={"Tmdb": "1"}),
            played(Type="Movie", Name="B", ProviderIds={"Tmdb": "2"}),
        ]
        counts, failures, _ = jellyfin.import_history(
            self.user, FakeClient(items), "shyan"
        )
        self.assertEqual(counts["Movie"], 1)
        self.assertEqual(len(failures), 1)
        self.assertIn("boom", failures[0])

    def test_unknown_user(self):
        """A Jellyfin username that doesn't exist is a clear error."""
        with self.assertRaises(jellyfin.JellyfinError):
            jellyfin.import_history(self.user, FakeClient([]), "nobody")


class MovieRewatchTests(TestCase):
    """A finished movie is only logged again when it's finished again."""

    def setUp(self):
        """Create a user with a completed movie."""
        self.user = get_user_model().objects.create_user(username="shyan")
        item = Item.objects.create(
            media_id="438631",
            source=Sources.TMDB.value,
            media_type=MediaTypes.MOVIE.value,
            title="Dune",
            image="",
        )
        Movie.objects.create(
            item=item,
            user=self.user,
            status=Status.COMPLETED.value,
            progress=1,
            end_date=timezone.now(),
        )

    @patch(
        "app.providers.tmdb.movie",
        return_value={"title": "Dune", "image": "", "max_progress": 1},
    )
    def test_skimming_or_importing_adds_nothing(self, _mock):
        """A start event or an import of a watched movie adds no entry."""
        start = {"Event": "Play", "Item": {"UserData": {"Played": False}}}
        stop = {"Event": "Stop", "Item": {"UserData": {"Played": True}}}
        JellyfinWebhookProcessor()._handle_movie("438631", start, self.user)
        JellyfinWebhookProcessor(import_mode=True)._handle_movie(
            "438631", stop, self.user
        )
        self.assertEqual(Movie.objects.filter(user=self.user).count(), 1)

        # Finishing it again live is a real rewatch.
        JellyfinWebhookProcessor()._handle_movie("438631", stop, self.user)
        self.assertEqual(Movie.objects.filter(user=self.user).count(), 2)
