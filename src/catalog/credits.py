"""Fetch and store cast for shows, movies and anime.

TV and movies come from TMDB (aggregate credits for shows, so the cast spans
every season). Anime comes from AniList: characters with their Japanese and
English voice actors. Credits are stored per ``app.Item`` so the library can
be queried for people who appear across it.
"""

import datetime
import logging

from django.db import transaction
from django.utils import timezone

from app.models import Item, MediaTypes, Sources
from app.providers import anilist, services, tmdb
from catalog.models import Credit, CreditFetch, CreditRole, Person

logger = logging.getLogger(__name__)

TMDB_PROFILE = "https://image.tmdb.org/t/p/w185"
MAX_CAST = 40
MAX_CHARACTERS = 50
STALE_AFTER = datetime.timedelta(days=30)
SUPPORTED = {
    MediaTypes.TV.value: {Sources.TMDB.value},
    MediaTypes.MOVIE.value: {Sources.TMDB.value},
    MediaTypes.ANIME.value: {Sources.MAL.value, Sources.ANILIST.value},
}
MOVIE_CREW_JOBS = {
    "Director",
    "Screenplay",
    "Writer",
    "Novel",
    "Original Music Composer",
}
TV_CREW_JOBS = {"Director", "Original Music Composer", "Novel"}
TV_CREW_MIN_EPISODES = 3
MAX_CREW = 8
VOICE_LANGUAGES = {"japanese", "english"}

ANILIST_CAST = """
query ($id: Int, $idMal: Int, $page: Int) {
  Media(id: $id, idMal: $idMal, type: ANIME) {
    characters(sort: [ROLE, RELEVANCE, ID], page: $page, perPage: 25) {
      pageInfo { hasNextPage }
      edges {
        role
        node { id name { full native } image { medium } }
        voiceActors(sort: [RELEVANCE, ID]) {
          id languageV2 name { full native } image { medium }
        }
      }
    }
  }
}
"""


def supports(item):
    """Whether cast can be fetched for this item."""
    return item.source in SUPPORTED.get(item.media_type, set())


def credit_item(item):
    """The item credits hang off: seasons use their show."""  # noqa: D401
    if item.media_type != MediaTypes.SEASON.value:
        return item
    show, _ = Item.objects.get_or_create(
        media_id=item.media_id,
        source=item.source,
        media_type=MediaTypes.TV.value,
        season_number=None,
        episode_number=None,
        defaults={"title": item.title, "image": item.image},
    )
    return show


def _person(source, external_id, name, image="", native=""):
    person, created = Person.objects.get_or_create(
        source=source,
        external_id=str(external_id),
        defaults={"name": name, "image": image or "", "native_name": native or ""},
    )
    if not created and (person.name != name or (image and person.image != image)):
        person.name = name
        person.image = image or person.image
        person.native_name = native or person.native_name
        person.save(update_fields=["name", "image", "native_name"])
    return person


def _tmdb_credits(item):
    kind = "tv" if item.media_type == MediaTypes.TV.value else "movie"
    endpoint = "aggregate_credits" if kind == "tv" else "credits"
    data = services.api_request(
        Sources.TMDB.value,
        "GET",
        f"{tmdb.base_url}/{kind}/{item.media_id}/{endpoint}",
        params=tmdb.base_params,
    )
    credits = []  # noqa: A001
    for order, cast in enumerate(data.get("cast", [])[:MAX_CAST]):
        roles = cast.get("roles") or [{"character": cast.get("character", "")}]
        character = " / ".join(
            r.get("character") or "" for r in roles if r.get("character")
        )
        image = (
            f"{TMDB_PROFILE}{cast['profile_path']}" if cast.get("profile_path") else ""
        )
        credits.append(
            (
                _person(Sources.TMDB.value, cast["id"], cast["name"], image),
                {
                    "role": CreditRole.ACTOR,
                    "character": character[:255],
                    "order": order,
                    "episode_count": cast.get("total_episode_count"),
                },
            ),
        )
    jobs_allowed = TV_CREW_JOBS if kind == "tv" else MOVIE_CREW_JOBS
    crew_rows = []
    for crew in data.get("crew", []):
        jobs = crew.get("jobs") or [{"job": crew.get("job"), "episode_count": None}]
        for job in jobs:
            episodes = job.get("episode_count")
            if job.get("job") not in jobs_allowed:
                continue
            # A show's aggregate crew lists every one-off episode director.
            if kind == "tv" and (episodes or 0) < TV_CREW_MIN_EPISODES:
                continue
            crew_rows.append((episodes or 0, crew, job.get("job")))
    crew_rows.sort(key=lambda row: -row[0])
    seen = set()
    for episodes, crew, job in crew_rows:
        if (crew["id"], job) in seen or len(seen) >= MAX_CREW:
            continue
        seen.add((crew["id"], job))
        image = (
            f"{TMDB_PROFILE}{crew['profile_path']}" if crew.get("profile_path") else ""
        )
        credits.append(
            (
                _person(Sources.TMDB.value, crew["id"], crew["name"], image),
                {
                    "role": CreditRole.CREW,
                    "job": job,
                    "order": len(credits),
                    "episode_count": episodes or None,
                },
            ),
        )
    return credits


def _anilist_credits(item):
    key = "id" if item.source == Sources.ANILIST.value else "idMal"
    credits = []  # noqa: A001
    page = 1
    order = 0
    while page <= MAX_CHARACTERS // 25:
        data = anilist.graphql_request(
            ANILIST_CAST,
            {key: int(item.media_id), "page": page},
        )
        characters = ((data.get("Media") or {}).get("characters")) or {}
        for edge in characters.get("edges", []):
            node = edge["node"]
            for actor in edge.get("voiceActors") or []:
                language = (actor.get("languageV2") or "").lower()
                if language not in VOICE_LANGUAGES:
                    continue
                credits.append(
                    (
                        _person(
                            Sources.ANILIST.value,
                            actor["id"],
                            actor["name"]["full"],
                            (actor.get("image") or {}).get("medium", ""),
                            actor["name"].get("native") or "",
                        ),
                        {
                            "role": CreditRole.VOICE,
                            "character": node["name"]["full"][:255],
                            "character_image": (node.get("image") or {}).get(
                                "medium", ""
                            )
                            or "",
                            "character_role": edge.get("role") or "",
                            "language": language[:20],
                            "order": order,
                        },
                    ),
                )
            order += 1
        if not characters.get("pageInfo", {}).get("hasNextPage"):
            break
        page += 1
    return credits


def fetch(item, *, force=False):
    """Fetch and store credits for an item unless they're fresh. Returns the item used."""  # noqa: E501, W505
    item = credit_item(item)
    if not supports(item):
        return item
    existing = CreditFetch.objects.filter(item=item).first()
    if existing and not force and timezone.now() - existing.fetched_at < STALE_AFTER:
        return item
    try:
        if item.media_type == MediaTypes.ANIME.value:
            credits = _anilist_credits(item)  # noqa: A001
        else:
            credits = _tmdb_credits(item)  # noqa: A001
    except (services.ProviderAPIError, KeyError, TypeError, ValueError) as exc:
        logger.warning("Credits unavailable for %s: %s", item, exc)
        CreditFetch.objects.update_or_create(
            item=item, defaults={"error": str(exc)[:255]}
        )
        return item
    with transaction.atomic():
        Credit.objects.filter(item=item).delete()
        Credit.objects.bulk_create(
            [Credit(item=item, person=p, **fields) for p, fields in credits]
        )
        CreditFetch.objects.update_or_create(
            item=item,
            defaults={"count": len(credits), "error": ""},
        )
    return item


def missing_items(items):
    """Items (from a queryset) whose credits were never fetched."""
    return [
        item for item in items if supports(item) and not hasattr(item, "credit_fetch")
    ]
