"""Cast and crew per title, and the people who recur across your library."""

from collections import defaultdict

from django.apps import apps
from django.core.cache import cache
from django.db.models import Count
from django.shortcuts import get_object_or_404
from ninja import Router
from ninja.errors import HttpError

from api.serializers import item_dict
from app.models import Item, Sources, Status
from app.providers import services
from catalog import credits as credit_service
from catalog.models import Credit, CreditRole, Person
from catalog.tasks import CAST_TYPES, backfill_credits

router = Router(tags=["people"])
BACKFILL_LOCK = "catalog:backfill_lock:{user_id}"


def person_dict(person):
    """Serialize a person."""
    if person.source == Sources.TMDB.value:
        url = f"https://www.themoviedb.org/person/{person.external_id}"
    else:
        url = f"https://anilist.co/staff/{person.external_id}"
    return {
        "id": person.id,
        "name": person.name,
        "native_name": person.native_name,
        "image": person.image or None,
        "source": person.source,
        "url": url,
    }


def watched_entries(user):
    """Item id -> (media_type, entry) for shows, movies and anime the user has started."""  # noqa: E501, W505
    entries = {}
    for kind in CAST_TYPES:
        queryset = (
            apps.get_model("app", kind)
            .objects.filter(user=user)
            .exclude(status=Status.PLANNING.value)
            .select_related("item")
        )
        for entry in queryset:
            entries[entry.item_id] = (kind, entry)
    return entries


def _entry_ref(kind, entry):
    return {
        "media_type": kind,
        "id": entry.id,
        "status": entry.status,
        "score": entry.score,
    }


@router.get("/credits/{media_type}/{source}/{media_id}")
def title_credits(request, media_type: str, source: str, media_id: str):  # noqa: C901
    """Cast (and key crew) of one title; fetched from the provider on first view.

    For anime, ``characters`` pairs each character with its Japanese and
    English voice actors. Each person carries ``also_in``: other titles in
    your watched library they appear in.
    """
    item = Item.objects.filter(
        media_type=media_type,
        source=source,
        media_id=media_id,
        season_number=None,
    ).first()
    if item is None:
        item = Item(media_type=media_type, source=source, media_id=media_id, title="")
        if not credit_service.supports(item):
            return {"supported": False, "cast": [], "crew": [], "characters": []}
        # Credits need a saved item; take title and image from metadata.
        try:
            meta = services.get_media_metadata(media_type, media_id, source)
        except services.ProviderAPIError as exc:
            raise HttpError(502, "Metadata unavailable") from exc
        item, _ = Item.objects.get_or_create(
            media_type=media_type,
            source=source,
            media_id=media_id,
            season_number=None,
            episode_number=None,
            defaults={"title": meta["title"], "image": meta["image"]},
        )
    if not credit_service.supports(credit_service.credit_item(item)):
        return {"supported": False, "cast": [], "crew": [], "characters": []}

    item = credit_service.fetch(item)
    rows = list(Credit.objects.filter(item=item).select_related("person"))
    library = watched_entries(request.user)
    others = defaultdict(list)
    person_ids = {row.person_id for row in rows}
    for credit in (
        Credit.objects.filter(person_id__in=person_ids, item_id__in=library.keys())
        .exclude(item=item)
        .select_related("item")
    ):
        kind, entry = library[credit.item_id]
        others[credit.person_id].append(
            {
                "item": item_dict(credit.item),
                "entry": _entry_ref(kind, entry),
                "character": credit.character or credit.job,
            },
        )

    def with_person(row):
        seen = set()
        also = []
        for other in others[row.person_id]:
            if other["item"]["id"] not in seen:
                seen.add(other["item"]["id"])
                also.append(other)
        return {"person": person_dict(row.person), "also_in": also}

    cast = [
        {
            **with_person(row),
            "character": row.character,
            "episode_count": row.episode_count,
        }
        for row in rows
        if row.role == CreditRole.ACTOR
    ]
    crew = [
        {**with_person(row), "job": row.job, "episode_count": row.episode_count}
        for row in rows
        if row.role == CreditRole.CREW
    ]
    characters = {}
    for row in rows:
        if row.role != CreditRole.VOICE:
            continue
        character = characters.setdefault(
            (row.order, row.character),
            {
                "character": row.character,
                "image": row.character_image or None,
                "role": row.character_role.lower(),
                "voice_actors": [],
            },
        )
        character["voice_actors"].append({**with_person(row), "language": row.language})
    return {
        "supported": True,
        "cast": cast,
        "crew": crew,
        "characters": list(characters.values()),
        "languages": sorted({row.language for row in rows if row.language}),
    }


def _kick_backfill(user):
    """Queue a cast backfill for this user's library at most every 10 minutes."""
    key = BACKFILL_LOCK.format(user_id=user.id)
    if cache.add(key, 1, 600):
        backfill_credits.delay(user_id=user.id)


@router.get("/people")
def library_people(
    request,
    role: str | None = None,
    language: str | None = None,
    min_titles: int = 2,
    limit: int = 60,
):
    """People who appear in at least ``min_titles`` titles you've watched.

    ``role`` is ``actor``, ``voice`` or ``crew``; ``language`` filters voice
    actors (e.g. ``japanese``). ``pending`` counts titles whose cast hasn't
    been fetched yet (a background job fills them in).
    """
    library = watched_entries(request.user)
    items = Item.objects.filter(id__in=library.keys()).select_related("credit_fetch")
    pending = len(credit_service.missing_items(items))
    if pending:
        _kick_backfill(request.user)

    credits = Credit.objects.filter(item_id__in=library.keys())  # noqa: A001
    if role:
        credits = credits.filter(role=role)  # noqa: A001
    if language:
        credits = credits.filter(language=language)  # noqa: A001
    top = (
        credits.values("person_id")
        .annotate(titles=Count("item_id", distinct=True))
        .filter(titles__gte=max(1, min_titles))
        .order_by("-titles")[: max(1, min(limit, 200))]
    )
    counts = {row["person_id"]: row["titles"] for row in top}
    people = Person.objects.in_bulk(counts.keys())
    appearances = defaultdict(dict)
    for credit in credits.filter(person_id__in=counts.keys()).select_related("item"):
        kind, entry = library[credit.item_id]
        current = appearances[credit.person_id].setdefault(
            credit.item_id,
            {
                "item": item_dict(credit.item),
                "entry": _entry_ref(kind, entry),
                "characters": [],
                "role": credit.role,
            },
        )
        label = credit.character or credit.job
        if label and label not in current["characters"]:
            current["characters"].append(label)

    results = [
        {
            "person": person_dict(people[person_id]),
            "titles": count,
            "appearances": list(appearances[person_id].values()),
        }
        for person_id, count in counts.items()
        if person_id in people
    ]
    results.sort(key=lambda r: (-r["titles"], r["person"]["name"]))
    return {
        "pending": pending,
        "library_titles": len(library),
        "results": results,
    }


@router.get("/people/{person_id}")
def person(request, person_id: int):
    """One person: their roles in your library and other titles we know of."""
    found = get_object_or_404(Person, id=person_id)
    library = watched_entries(request.user)
    planned = {}
    for kind in CAST_TYPES:
        for entry in (
            apps.get_model("app", kind)
            .objects.filter(user=request.user, status=Status.PLANNING.value)
            .select_related("item")
        ):
            planned[entry.item_id] = (kind, entry)

    in_library, elsewhere = {}, {}
    for credit in Credit.objects.filter(person=found).select_related("item"):
        ref = library.get(credit.item_id) or planned.get(credit.item_id)
        bucket = in_library if credit.item_id in library else elsewhere
        current = bucket.setdefault(
            credit.item_id,
            {
                "item": item_dict(credit.item),
                "entry": _entry_ref(*ref) if ref else None,
                "characters": [],
                "character_images": [],
                "role": credit.role,
                "language": credit.language,
            },
        )
        label = credit.character or credit.job
        if label and label not in current["characters"]:
            current["characters"].append(label)
            if credit.character_image:
                current["character_images"].append(credit.character_image)
    return {
        "person": person_dict(found),
        "in_library": list(in_library.values()),
        "elsewhere": list(elsewhere.values()),
    }


@router.get("/people/search/by-name")
def people_by_name(request, q: str):  # noqa: ARG001
    """Find people already in the catalog by name."""
    if len(q.strip()) < 2:  # noqa: PLR2004
        raise HttpError(400, "Query too short")
    found = Person.objects.filter(name__icontains=q.strip()).order_by("name")[:30]
    return [person_dict(p) for p in found]
