"""Shows (seasons, episodes, specials) and the year snapshot."""

import datetime
from unittest.mock import patch

from django.contrib.auth import get_user_model
from django.core.cache import cache
from django.test import TestCase
from django.utils import timezone

from api.models import ApiToken, TokenScope
from app.models import Anime, Item, MediaTypes, Season, Sources, Status

TV_META = {
    "title": "Severance",
    "image": "https://img/sev.jpg",
    "max_progress": 19,
    "genres": ["Drama"],
    "details": {"status": "Returning Series"},
    "related": {
        "seasons": [
            {"season_number": 0, "season_title": "Specials", "max_progress": 1},
            {"season_number": 1, "season_title": "Season 1", "max_progress": 3},
            {"season_number": 2, "season_title": "Season 2", "max_progress": 2},
        ],
    },
}


def season_meta(number, count):
    """Season metadata with ``count`` aired episodes."""
    return {
        "title": "Severance",
        "image": "https://img/sev.jpg",
        "season_title": "Specials" if number == 0 else f"Season {number}",
        "max_progress": count,
        "episodes": [
            {
                "episode_number": n,
                "name": f"Ep {n}",
                "air_date": "2022-02-18",
                "runtime": 50,
                "still_path": None,
                "overview": "",
            }
            for n in range(1, count + 1)
        ],
    }


def fake_metadata(media_type, media_id, source, season_numbers=None):  # noqa: ARG001
    """Stand-in for provider metadata."""
    if media_type in {MediaTypes.SEASON.value, "tv_with_seasons"}:
        number = season_numbers[0]
        count = {0: 1, 1: 3, 2: 2}[number]
        data = season_meta(number, count)
        if media_type == "tv_with_seasons":
            return {**TV_META, f"season/{number}": data}
        return data
    if media_type == MediaTypes.TV.value:
        return TV_META
    return {"title": "Frieren", "image": "", "max_progress": 28, "genres": ["Fantasy"]}


@patch("app.models.Item.fetch_releases")
@patch("app.providers.services.get_media_metadata", side_effect=fake_metadata)
class ShowTests(TestCase):
    """Browse every season and mark episodes."""

    def setUp(self):
        """Create a user with a write token."""
        cache.clear()
        self.user = get_user_model().objects.create_user(
            username="u",
            password="pw-12345678",  # noqa: S106
        )
        _, key = ApiToken.objects.create_token(self.user, "t", TokenScope.WRITE)
        self.auth = {"HTTP_AUTHORIZATION": f"Bearer {key}"}

    def url(self, suffix=""):
        """Show URL."""
        return f"/api/v1/shows/{Sources.TMDB.value}/95396{suffix}"

    def test_specials_listed_last(self, *_):
        """Specials come after the regular seasons and don't count to completion."""
        data = self.client.get(self.url(), **self.auth).json()
        self.assertEqual([s["season_number"] for s in data["seasons"]], [1, 2, 0])
        self.assertTrue(data["seasons"][-1]["is_special"])
        self.assertEqual(data["completion"]["total"], 5)

    def test_mark_up_to_and_unwatch(self, *_):
        """Marking creates the season; unwatching removes episodes."""
        mark = self.url("/seasons/1/episodes")
        data = self.client.post(
            mark, {"up_to": 2}, content_type="application/json", **self.auth
        ).json()
        self.assertEqual(data["watched"], 2)
        self.assertTrue(
            Season.objects.filter(user=self.user, item__season_number=1).exists()
        )
        data = self.client.post(
            mark,
            {"episodes": [2], "watched": False},
            content_type="application/json",
            **self.auth,
        ).json()
        self.assertEqual(data["watched"], 1)
        show = self.client.get(self.url(), **self.auth).json()
        self.assertEqual(show["completion"]["watched"], 1)

    def test_specials_can_be_marked(self, *_):
        """Season 0 is markable like any other."""
        data = self.client.post(
            self.url("/seasons/0/episodes"),
            {"all": True},
            content_type="application/json",
            **self.auth,
        ).json()
        self.assertEqual((data["watched"], data["total"]), (1, 1))
        self.assertEqual(
            self.client.get(self.url(), **self.auth).json()["completion"][
                "specials_watched"
            ],
            1,
        )


@patch("app.models.Item.fetch_releases")
@patch("app.providers.services.get_media_metadata", side_effect=fake_metadata)
class YearTests(TestCase):
    """Year filter and snapshot."""

    def setUp(self):
        """Create an anime watched last year."""
        cache.clear()
        self.user = get_user_model().objects.create_user(
            username="y",
            password="pw-12345678",  # noqa: S106
        )
        _, key = ApiToken.objects.create_token(self.user, "t")
        self.auth = {"HTTP_AUTHORIZATION": f"Bearer {key}"}
        self.year = timezone.localdate().year - 1
        item = Item.objects.create(
            media_id="52991",
            source=Sources.MAL.value,
            media_type=MediaTypes.ANIME.value,
            title="Frieren",
        )
        anime = Anime(
            item=item,
            user=self.user,
            status=Status.COMPLETED.value,
            progress=28,
            score=9,
            start_date=timezone.make_aware(datetime.datetime(self.year, 3, 1)),  # noqa: DTZ001
            end_date=timezone.make_aware(datetime.datetime(self.year, 6, 1)),  # noqa: DTZ001
        )
        anime.save()

    def test_library_year_filter(self, *_):
        """Entries are filtered to years they were active in."""
        data = self.client.get(
            f"/api/v1/library?media_type=anime&year={self.year}", **self.auth
        ).json()
        self.assertEqual(data["count"], 1)
        self.assertIn(self.year, data["years"])
        other = self.client.get(
            f"/api/v1/library?media_type=anime&year={self.year - 3}", **self.auth
        ).json()
        self.assertEqual(other["count"], 0)

    def test_snapshot(self, *_):
        """The snapshot lists finishes, top rated and genres."""
        data = self.client.get(f"/api/v1/years/{self.year}", **self.auth).json()
        self.assertEqual(data["totals"]["finished"], 1)
        self.assertEqual(data["top_rated"][0]["item"]["title"], "Frieren")
        self.assertEqual(data["genres"][0]["name"], "Fantasy")
        self.assertIn(self.year, self.client.get("/api/v1/years", **self.auth).json())
