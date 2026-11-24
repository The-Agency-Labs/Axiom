"""Plain-dict serializers shared by the API routers."""

import datetime
import decimal

from django.utils.functional import Promise


def jsonable(value):  # noqa: PLR0911 one return per type
    """Recursively convert a value into JSON-safe primitives.

    Provider metadata and history entries contain lazy translation strings,
    decimals, model instances and datetimes; this flattens all of them.
    """
    if value is None or isinstance(value, (bool, int, float, str)):
        return value
    if isinstance(value, Promise):
        return str(value)
    if isinstance(value, decimal.Decimal):
        return float(value)
    if isinstance(value, (datetime.datetime, datetime.date)):
        return value.isoformat()
    if isinstance(value, dict):
        return {str(k): jsonable(v) for k, v in value.items()}
    if isinstance(value, (list, tuple, set)):
        return [jsonable(v) for v in value]
    if hasattr(value, "_meta") and hasattr(value, "pk"):
        if value._meta.model_name == "item":
            return item_dict(value)
        return str(value)
    return str(value)


def item_dict(item):
    """Serialize an ``app.Item``."""
    if item is None:
        return None
    return {
        "id": item.id,
        "media_id": item.media_id,
        "source": item.source,
        "media_type": item.media_type,
        "title": item.title,
        "image": item.image,
        "season_number": item.season_number,
        "episode_number": item.episode_number,
    }


def media_dict(media, media_type):
    """Serialize a user's library entry of any media type."""
    data = {
        "id": media.id,
        "media_type": media_type,
        "item": item_dict(media.item),
        "status": media.status,
        "score": jsonable(media.score),
        "progress": media.progress,
        "progressed_at": jsonable(getattr(media, "progressed_at", None)),
        "start_date": jsonable(media.start_date),
        "end_date": jsonable(media.end_date),
        "notes": media.notes,
        "created_at": jsonable(media.created_at),
    }
    max_progress = getattr(media, "max_progress", None)
    if max_progress is not None:
        data["max_progress"] = max_progress
    return data


def episode_dict(episode):
    """Serialize a watched episode."""
    return {
        "episode_number": episode.item.episode_number,
        "title": episode.item.title,
        "watched_at": jsonable(episode.end_date),
        "notes": getattr(episode, "notes", ""),
    }


def session_dict(session):
    """Serialize a reading session."""
    return {
        "id": session.id,
        "source": session.source,
        "title": session.title,
        "item": item_dict(session.item),
        "document_id": session.document_id,
        "started_at": jsonable(session.started_at),
        "ended_at": jsonable(session.ended_at),
        "minutes": round(session.duration_seconds / 60),
        "pages_read": session.pages_read,
        "chapters_read": session.chapters_read,
        "start_page": session.start_page,
        "end_page": session.end_page,
        "device": session.device,
    }


def document_dict(document):
    """Serialize a device document."""
    return {
        "id": document.id,
        "document_hash": document.document_hash,
        "title": document.title,
        "authors": document.authors,
        "total_pages": document.total_pages,
        "percentage": document.percentage,
        "device": document.device,
        "progress_updated_at": jsonable(document.progress_updated_at),
        "item": item_dict(document.item),
    }
