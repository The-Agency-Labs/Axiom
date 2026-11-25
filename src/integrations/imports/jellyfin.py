"""Import watch history from a Jellyfin server.

Every played movie and episode is replayed through the Jellyfin webhook
processor, so matching (TVDB/IMDb/TMDB, anime via MAL mappings) is identical
to live tracking, with the real watch date from Jellyfin.

The import is additive only: it never deletes or lowers anything in Encompass.
Removing media from Jellyfin (to free space, say) changes nothing here.
"""

import logging
from collections import Counter

import requests
from django.conf import settings
from django.utils.dateparse import parse_datetime

from integrations.webhooks.jellyfin import JellyfinWebhookProcessor

logger = logging.getLogger(__name__)

PAGE_SIZE = 500


class JellyfinError(Exception):
    """Jellyfin could not be reached or refused the request."""


class JellyfinClient:
    """Minimal read-only Jellyfin API client using an API key."""

    def __init__(self, url, api_key):
        """Connect to the server at `url` with an API key from its dashboard."""
        if not url or not api_key:
            msg = "JELLYFIN_URL and JELLYFIN_API_KEY must be set"
            raise JellyfinError(msg)
        self.url = url.rstrip("/")
        self.session = requests.Session()
        self.session.headers["Authorization"] = f'MediaBrowser Token="{api_key}"'

    def get(self, path, **params):
        """GET a JSON endpoint."""
        try:
            response = self.session.get(
                f"{self.url}{path}",
                params=params,
                timeout=max(settings.REQUEST_TIMEOUT, 30),
            )
            response.raise_for_status()
        except requests.RequestException as exc:
            msg = f"Jellyfin request failed: {path}: {exc}"
            raise JellyfinError(msg) from exc
        return response.json()

    def user_id(self, username):
        """Look up the Jellyfin user ID for `username` (case-insensitive)."""
        for user in self.get("/Users"):
            if user["Name"].lower() == username.lower():
                return user["Id"]
        msg = f"No Jellyfin user named {username!r}"
        raise JellyfinError(msg)

    def items(self, user_id, item_types, **filters):
        """All items of the given types visible to the user, paged."""
        start = 0
        while True:
            page = self.get(
                "/Items",
                userId=user_id,
                Recursive="true",
                IncludeItemTypes=item_types,
                Fields="ProviderIds,ProductionYear",
                EnableImages="false",
                StartIndex=start,
                Limit=PAGE_SIZE,
                **filters,
            )
            yield from page.get("Items", [])
            start += PAGE_SIZE
            if start >= page.get("TotalRecordCount", 0):
                return


def _payload(item, series_ids):
    """Build a webhook-style payload for a played item."""
    payload = {
        "Event": "Stop",
        "Item": {
            "Type": item.get("Type"),
            "Name": item.get("Name"),
            "ProductionYear": item.get("ProductionYear"),
            "ProviderIds": item.get("ProviderIds") or {},
            "UserData": {"Played": True},
            "SeriesName": item.get("SeriesName"),
            "ParentIndexNumber": item.get("ParentIndexNumber"),
            "IndexNumber": item.get("IndexNumber"),
        },
    }
    if item.get("SeriesId") in series_ids:
        payload["Series"] = {"ProviderIds": series_ids[item["SeriesId"]]}
    return payload


def _label(item):
    if item.get("Type") == "Episode":
        return (
            f"{item.get('SeriesName')} "
            f"S{item.get('ParentIndexNumber') or 0:02d}"
            f"E{item.get('IndexNumber') or 0:02d}"
        )
    return f"{item.get('Name')} ({item.get('ProductionYear') or '?'})"


def import_history(user, client, jellyfin_user, since=None):
    """Replay Jellyfin's played movies and episodes into `user`'s library.

    Args:
        user: the Encompass user.
        client: a JellyfinClient.
        jellyfin_user: the Jellyfin username whose history to import.
        since: only items last played after this datetime (incremental runs).

    Returns:
        (Counter of processed item types, list of failure descriptions,
        latest LastPlayedDate seen or None).
    """
    jf_user_id = client.user_id(jellyfin_user)
    series_ids = {
        series["Id"]: series.get("ProviderIds") or {}
        for series in client.items(jf_user_id, "Series")
    }
    played = [
        item
        for item in client.items(jf_user_id, "Movie,Episode", Filters="IsPlayed")
        if item.get("UserData", {}).get("Played")
    ]

    def last_played(item):
        return parse_datetime(item["UserData"].get("LastPlayedDate") or "") or None

    if since:
        played = [item for item in played if (last_played(item) or since) > since]
    # Oldest first, so progress and dates build up in watch order.
    played.sort(
        key=lambda item: (
            item["UserData"].get("LastPlayedDate") or "",
            item.get("ParentIndexNumber") or 0,
            item.get("IndexNumber") or 0,
        )
    )

    counts = Counter()
    failures = []
    latest = None
    for item in played:
        watched_at = last_played(item)
        processor = JellyfinWebhookProcessor(watched_at, import_mode=True)
        try:
            processor.process_payload(_payload(item, series_ids), user)
        except Exception as exc:  # one bad item mustn't stop the import
            logger.warning("Jellyfin import failed for %s", _label(item), exc_info=True)
            failures.append(f"{_label(item)}: {exc}")
            continue
        counts[item["Type"]] += 1
        if watched_at and (latest is None or watched_at > latest):
            latest = watched_at
    return counts, failures, latest
