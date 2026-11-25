"""Anime mappings, anime episodes, cast credits and the people API."""

from unittest.mock import patch

from django.contrib.auth import get_user_model
from django.core.cache import cache
from django.test import TestCase

from api.models import ApiToken, TokenScope
from app.models import Anime, Item, MediaTypes, Movie, Sources, Status
from catalog import credits, mappings  # noqa: A004
from catalog.anime import anime_episodes
from catalog.models import Credit

DATASET = [
    {
        "type": "TV",
        "mal_id": 100,
        "anilist_id": 200,
        "themoviedb_id": {"tv": 300},
        "season": {"tmdb": 1},
        "episode_offset": {"tmdb": 2},
    },
    {"type": "MOVIE", "mal_id": 101, "themoviedb_id": {"movie": [400]}},
    {"type": "TV", "anilist_id": 201},
]

TMDB_SEASON = {
    "title": "Show",
    "image": "",
    "episodes": [
        {
            "episode_number": n,
            "name": f"TMDB {n}",
            "overview": f"Plot {n}",
            "still_path": f"/s{n}.jpg",
            "air_date": "2024-01-01",
            "runtime": 24,
            "vote_average": 8,
        }
        for n in range(1, 6)
    ],
}

ANILIST_EPISODES = {
    "Media": {
        "id": 200,
        "episodes": 3,
        "streamingEpisodes": [
            {
                "title": "Episode 3 - Third",
                "thumbnail": "https://cr/3.jpg",
                "site": "CR",
            },
            {
                "title": "Episode 1 - First",
                "thumbnail": "https://cr/1.jpg",
                "site": "CR",
            },
        ],
    },
}


def anilist_reply(query, variables):  # noqa: ARG001
    """Fake AniList: episodes or characters depending on the query."""
    if "characters" in query:
        return {
            "Media": {
                "characters": {
                    "pageInfo": {"hasNextPage": False},
                    "edges": [
                        {
                            "role": "MAIN",
                            "node": {
                                "id": 1,
                                "name": {"full": "Hero", "native": ""},
                                "image": {"medium": "c.jpg"},
                            },
                            "voiceActors": [
                                {
                                    "id": 9,
                                    "languageV2": "Japanese",
                                    "name": {"full": "VA One", "native": "声"},
                                    "image": {"medium": "v.jpg"},
                                },
                                {
                                    "id": 10,
                                    "languageV2": "Italian",
                                    "name": {"full": "VA Two", "native": ""},
                                    "image": {},
                                },
                            ],
                        },
                    ],
                },
            },
        }
    return ANILIST_EPISODES


class MappingTests(TestCase):
    """Loading and looking up mappings."""

    def test_refresh_and_lookup(self):
        """Rows load from the dataset and resolve by MAL or AniList id."""
        self.assertEqual(mappings.refresh(DATASET), 3)
        mapping = mappings.lookup("mal", "100")
        self.assertEqual(
            (mapping.tmdb_tv, mapping.tmdb_season, mapping.tmdb_offset), (300, 1, 2)
        )
        self.assertEqual(mappings.lookup("anilist", "200"), mapping)
        self.assertEqual(mappings.lookup("mal", "101").tmdb_movie, 400)
        self.assertIsNone(mappings.lookup("mal", "999"))


@patch("app.providers.anilist.graphql_request", side_effect=anilist_reply)
@patch("app.providers.services.get_media_metadata", return_value=TMDB_SEASON)
class AnimeEpisodeTests(TestCase):
    """Episode resolution order."""

    def setUp(self):
        """Load mappings."""
        cache.clear()
        mappings.refresh(DATASET)

    def test_tmdb_with_offset_and_anilist_merge(self, *_):
        """TMDB episodes start after the offset; AniList fills nothing it has."""
        episodes, info = anime_episodes("mal", "100", 3)
        self.assertEqual([e["title"] for e in episodes], ["TMDB 3", "TMDB 4", "TMDB 5"])
        self.assertEqual(info["providers"], ["tmdb", "anilist"])
        self.assertTrue(episodes[0]["image"].endswith("/s3.jpg"))

    def test_anilist_only(self, metadata, _):
        """Without a TMDB mapping, AniList titles and thumbnails are used."""
        episodes, info = anime_episodes("anilist", "201", 3)
        metadata.assert_not_called()
        self.assertEqual(info["providers"], ["anilist"])
        self.assertEqual(episodes[0]["title"], "First")
        self.assertEqual(episodes[1]["title"], "Episode 2")
        self.assertEqual(episodes[2]["image"], "https://cr/3.jpg")

    def test_movies_skip_tmdb_tv(self, metadata, _):
        """Anime films aren't matched to a TV season."""
        anime_episodes("mal", "101", 1)
        metadata.assert_not_called()


class CreditTests(TestCase):
    """Fetching and storing cast."""

    @patch("app.providers.anilist.graphql_request", side_effect=anilist_reply)
    def test_anime_voice_cast(self, _):
        """Only Japanese and English voice actors are kept."""
        item = Item.objects.create(
            media_id="100",
            source=Sources.MAL.value,
            media_type=MediaTypes.ANIME.value,
            title="A",
        )
        credits.fetch(item)
        rows = list(Credit.objects.filter(item=item))
        self.assertEqual(len(rows), 1)
        self.assertEqual(
            (rows[0].person.name, rows[0].language, rows[0].character),
            ("VA One", "japanese", "Hero"),
        )

    @patch("app.providers.services.api_request")
    def test_movie_cast_and_crew(self, request):
        """Movie cast plus directors and writers."""
        request.return_value = {
            "cast": [
                {"id": 1, "name": "Actor", "character": "Lead", "profile_path": None}
            ],
            "crew": [
                {"id": 2, "name": "Dir", "job": "Director", "profile_path": None},
                {"id": 3, "name": "Grip", "job": "Key Grip", "profile_path": None},
            ],
        }
        item = Item.objects.create(
            media_id="5",
            source=Sources.TMDB.value,
            media_type=MediaTypes.MOVIE.value,
            title="M",
        )
        credits.fetch(item)
        self.assertEqual(
            sorted(
                Credit.objects.filter(item=item).values_list("person__name", flat=True)
            ),
            ["Actor", "Dir"],
        )
        credits.fetch(item)  # fresh: no second request
        self.assertEqual(request.call_count, 1)


@patch("app.models.Item.fetch_releases")
@patch(
    "app.providers.services.get_media_metadata",
    return_value={"title": "T", "image": "", "max_progress": 3},
)
@patch("app.providers.anilist.graphql_request", side_effect=anilist_reply)
class PeopleApiTests(TestCase):
    """People across the library, and anime episode marking."""

    def setUp(self):
        """User, token, two watched titles sharing an actor."""
        cache.clear()
        mappings.refresh(DATASET)
        self.user = get_user_model().objects.create_user(
            username="p",
            password="pw-12345678",  # noqa: S106
        )  # noqa: RUF100, S106
        _, key = ApiToken.objects.create_token(self.user, "t", TokenScope.WRITE)
        self.auth = {"HTTP_AUTHORIZATION": f"Bearer {key}"}

    def _watched_movie(self, media_id):
        item = Item.objects.create(
            media_id=media_id,
            source=Sources.TMDB.value,
            media_type=MediaTypes.MOVIE.value,
            title=f"M{media_id}",
        )
        Movie.objects.create(
            item=item, user=self.user, status=Status.COMPLETED.value, progress=1
        )
        return item

    @patch("app.providers.services.api_request")
    def test_recurring_people(self, request, *_):
        """An actor in two watched movies is listed with both roles."""
        request.side_effect = [
            {
                "cast": [
                    {
                        "id": 7,
                        "name": "Walken",
                        "character": "Emperor",
                        "profile_path": None,
                    }
                ],
                "crew": [],
            },
            {
                "cast": [
                    {
                        "id": 7,
                        "name": "Walken",
                        "character": "Burt",
                        "profile_path": None,
                    }
                ],
                "crew": [],
            },
        ]
        for media_id in ("1", "2"):
            credits.fetch(self._watched_movie(media_id))
        data = self.client.get("/api/v1/people", **self.auth).json()
        self.assertEqual(data["pending"], 0)
        self.assertEqual(data["results"][0]["person"]["name"], "Walken")
        self.assertEqual(data["results"][0]["titles"], 2)
        person_id = data["results"][0]["person"]["id"]
        detail = self.client.get(f"/api/v1/people/{person_id}", **self.auth).json()
        self.assertEqual(len(detail["in_library"]), 2)
        cast = self.client.get("/api/v1/credits/movie/tmdb/1", **self.auth).json()
        self.assertEqual(cast["cast"][0]["also_in"][0]["character"], "Burt")

    def test_anime_episode_marking(self, *_):
        """Ticking episode N sets progress N and tracks the anime."""
        url = "/api/v1/anime/anilist/201/episodes"
        data = self.client.post(
            url, {"episode": 2}, content_type="application/json", **self.auth
        ).json()
        self.assertEqual(data["progress"], 2)
        self.assertEqual([e["watched"] for e in data["episodes"]], [True, True, False])
        self.assertIsNotNone(data["episodes"][0]["watched_at"])
        data = self.client.post(
            url,
            {"episode": 2, "watched": False},
            content_type="application/json",
            **self.auth,
        ).json()
        self.assertEqual(data["progress"], 1)
        data = self.client.post(
            url, {"episode": 3}, content_type="application/json", **self.auth
        ).json()
        self.assertEqual(
            Anime.objects.get(user=self.user).status, Status.COMPLETED.value
        )
