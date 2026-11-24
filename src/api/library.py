"""Library: search, metadata, and the user's tracked entries."""

import logging
from typing import Literal

from django.apps import apps
from django.shortcuts import get_object_or_404
from ninja import Router, Schema
from ninja.errors import HttpError

from api.auth import require_write
from api.serializers import episode_dict, jsonable, media_dict
from app.forms import get_form_class
from app.models import Item, MediaTypes, Sources
from app.providers import services

logger = logging.getLogger(__name__)
router = Router(tags=["library"])

MediaType = Literal[tuple(MediaTypes.values)]
# Entries the user tracks directly (episodes belong to seasons).
TRACKABLE = [t for t in MediaTypes.values if t != MediaTypes.EPISODE.value]


def _model(media_type):
    return apps.get_model(app_label="app", model_name=media_type)


def _metadata(media_type, source, media_id, season_number=None):
    try:
        return services.get_media_metadata(
            media_type,
            media_id,
            source,
            [season_number] if season_number is not None else None,
        )
    except services.ProviderAPIError as exc:
        raise HttpError(502, f"{source} metadata unavailable") from exc


def _history(entry):
    """Return the change log for an entry, oldest first."""
    if not hasattr(entry, "history"):
        return []
    records = list(entry.history.order_by("history_date"))
    return [
        {
            "date": jsonable(record.history_date),
            "type": {"+": "created", "~": "changed", "-": "deleted"}.get(
                record.history_type,
                record.history_type,
            ),
            "status": getattr(record, "status", None),
            "progress": getattr(record, "progress", None),
            "score": jsonable(getattr(record, "score", None)),
        }
        for record in records
    ]


@router.get("/search")
def search(
    request,
    media_type: MediaType,
    q: str,
    page: int = 1,
    source: str | None = None,
):
    """Search a metadata provider (TMDB, AniList, MAL, Hardcover, ...)."""
    try:
        results = services.search(
            media_type,
            q,
            page,
            source,
            show_adult_titles=getattr(request.user, "show_adult_titles", True),
        )
    except services.ProviderAPIError as exc:
        raise HttpError(502, "Search provider unavailable") from exc
    return jsonable(results)


@router.get("/metadata/{media_type}/{source}/{media_id}")
def metadata(
    request,  # noqa: ARG001
    media_type: MediaType,
    source: str,
    media_id: str,
    season_number: int | None = None,
):
    """Full provider metadata: synopsis, genres, cast, episode lists, page count."""
    if source not in Sources.values:
        raise HttpError(400, "Unknown source")
    return jsonable(_metadata(media_type, source, media_id, season_number))


@router.get("/library")
def list_library(
    request,
    media_type: MediaType | None = None,
    status: str | None = None,
    search: str | None = None,
    limit: int = 200,
    offset: int = 0,
):
    """List the user's tracked entries, newest activity first."""
    types = [media_type] if media_type else TRACKABLE
    results = []
    for kind in types:
        if kind == MediaTypes.EPISODE.value:
            continue
        queryset = (
            _model(kind)
            .objects.filter(user=request.user)
            .select_related(
                "item",
            )
        )
        if status:
            queryset = queryset.filter(status=status)
        if search:
            queryset = queryset.filter(item__title__icontains=search)
        results.extend(media_dict(entry, kind) for entry in queryset)

    results.sort(
        key=lambda e: e["progressed_at"] or e["created_at"] or "",
        reverse=True,
    )
    return {"count": len(results), "results": results[offset : offset + limit]}


@router.get("/library/{media_type}/{instance_id}")
def get_entry(
    request,
    media_type: MediaType,
    instance_id: int,
    include_metadata: bool = True,  # noqa: FBT001, FBT002 query parameter
):
    """One entry with its history, watched episodes and provider metadata."""
    entry = get_object_or_404(
        _model(media_type).objects.select_related("item"),
        id=instance_id,
        user=request.user,
    )
    data = media_dict(entry, media_type)
    data["history"] = _history(entry)

    if media_type == MediaTypes.TV.value:
        data["seasons"] = [
            {
                **media_dict(season, MediaTypes.SEASON.value),
                "episodes": [
                    episode_dict(ep)
                    for ep in season.episodes.select_related("item").order_by(
                        "item__episode_number",
                    )
                ],
            }
            for season in entry.seasons.select_related("item").order_by(
                "item__season_number",
            )
        ]
    elif media_type == MediaTypes.SEASON.value:
        data["episodes"] = [
            episode_dict(ep)
            for ep in entry.episodes.select_related("item").order_by(
                "item__episode_number",
            )
        ]

    if include_metadata:
        item = entry.item
        try:
            data["metadata"] = jsonable(
                _metadata(media_type, item.source, item.media_id, item.season_number),
            )
        except HttpError:
            data["metadata"] = None
    return data


class EntryIn(Schema):
    """Create or update a library entry."""

    media_type: MediaType
    source: str
    media_id: str
    season_number: int | None = None
    instance_id: int | None = None
    status: str | None = None
    progress: int | None = None
    score: float | None = None
    start_date: str | None = None
    end_date: str | None = None
    notes: str | None = None


@router.post("/library")
def save_entry(request, payload: EntryIn):
    """Track something new, or update an entry when ``instance_id`` is given."""
    require_write(request)
    media_type = payload.media_type
    if media_type == MediaTypes.EPISODE.value:
        raise HttpError(400, "Track episodes through their season")

    model = _model(media_type)
    if payload.instance_id:
        instance = get_object_or_404(model, id=payload.instance_id, user=request.user)
    else:
        meta = _metadata(
            media_type,
            payload.source,
            payload.media_id,
            payload.season_number,
        )
        item, _ = Item.objects.get_or_create(
            media_id=payload.media_id,
            source=payload.source,
            media_type=media_type,
            season_number=payload.season_number,
            defaults={"title": meta["title"], "image": meta["image"]},
        )
        instance = model(item=item, user=request.user)

    # Start from the current values so a partial update keeps the rest.
    form_class = get_form_class(media_type)
    data = {
        name: getattr(instance, name)
        for name in form_class._meta.fields
        if getattr(instance, name, None) is not None
    }
    data.update(
        {
            key: value
            for key, value in payload.dict().items()
            if value is not None and key in form_class._meta.fields
        },
    )
    data.update(
        media_type=media_type,
        source=instance.item.source,
        media_id=instance.item.media_id,
    )
    form = form_class(data, instance=instance)
    if not form.is_valid():
        raise HttpError(422, form.errors.as_json())
    form.save()
    return media_dict(form.instance, media_type)


@router.delete("/library/{media_type}/{instance_id}")
def delete_entry(request, media_type: MediaType, instance_id: int):
    """Remove an entry (and its history) from the library."""
    require_write(request)
    entry = get_object_or_404(_model(media_type), id=instance_id, user=request.user)
    entry.delete()
    return {"deleted": True}
