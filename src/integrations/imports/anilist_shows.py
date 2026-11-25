"""Import an AniList list, tracking anime as whole TMDB shows.

AniList splits a show into one entry per cour or season. Encompass tracks the
show itself, with its seasons and specials, so each anime entry is placed in
its TMDB show through the Fribb mapping table (season plus episode offset) and
its watched episodes are marked there. Anime films become movies. Entries the
mapping doesn't know, and all manga, use the regular AniList import.

The import is additive: it adds episodes and moves statuses forward, and never
removes or lowers anything already tracked. That makes it safe to re-run on a
schedule to pick up progress logged on AniList (by Paperback, say).

Watch dates, when AniList doesn't have a believable one:
  - series that started in 2012 or later were watched as they aired, so each
    episode gets its air date and the entry completes when the series ended;
  - older series get 1 January 2019.
A completion date shared by more than BULK_DATE_LIMIT entries is a date the
list was filled in on, not a watch date, and is ignored.
"""

import datetime
import logging
from collections import Counter, defaultdict

import requests
from django.utils import timezone
from django.utils.dateparse import parse_date
from simple_history.utils import bulk_create_with_history

import app
from app.models import TV, Episode, Item, MediaTypes, Movie, Season, Sources, Status
from catalog import mappings
from catalog.models import AnimeMapping
from integrations.imports import helpers
from integrations.imports.anilist import AniListImporter
from integrations.imports.helpers import MediaImportError

logger = logging.getLogger(__name__)

URL = "https://graphql.anilist.co"
BULK_DATE_LIMIT = 2
AIRED_SINCE_YEAR = 2012
OLD_SERIES_DATE = datetime.date(2019, 1, 1)
WATCHING = {"CURRENT", "REPEATING"}

_ENTRY_FIELDS = """
    status
    score(format: POINT_10_DECIMAL)
    progress
    repeat
    notes
    updatedAt
    startedAt { year month day }
    completedAt { year month day }
    media {
        id
        idMal
        format
        episodes
        chapters
        title { userPreferred }
        coverImage { large }
        startDate { year month day }
        endDate { year month day }
    }
"""

QUERY = f"""
query ($userName: String) {{
    anime: MediaListCollection(userName: $userName, type: ANIME) {{
        lists {{ isCustomList entries {{ {_ENTRY_FIELDS} }} }}
    }}
    manga: MediaListCollection(userName: $userName, type: MANGA) {{
        lists {{ isCustomList entries {{ {_ENTRY_FIELDS} }} }}
    }}
}}
"""


def fetch_lists(username):
    """Return (anime entries, manga entries) of a public AniList user."""
    try:
        response = app.providers.services.api_request(
            Sources.ANILIST.value,
            "POST",
            URL,
            params={"query": QUERY, "variables": {"userName": username}},
            headers={"Content-Type": "application/json", "Accept": "application/json"},
        )
    except requests.exceptions.HTTPError as error:
        msg = f"AniList user {username} could not be read: {error}"
        raise MediaImportError(msg) from error

    def entries(collection):
        return [
            entry
            for status_list in collection["lists"]
            if not status_list["isCustomList"]
            for entry in status_list["entries"]
        ]

    data = response["data"]
    return entries(data["anime"]), entries(data["manga"])


def _date(parts):
    if not parts or not parts.get("year"):
        return None
    return datetime.date(parts["year"], parts.get("month") or 1, parts.get("day") or 1)


def _at_noon(day):
    return timezone.make_aware(datetime.datetime.combine(day, datetime.time(12)))


def _datekey(parts):
    return (parts.get("year"), parts.get("month"), parts.get("day"))


class AniListShowImporter:
    """Import one AniList user's list into an Encompass user's library."""

    def __init__(self, user, username, since=None):
        """Import `username`'s list for `user`; `since` limits it to newer edits."""
        self.user = user
        self.username = username
        self.since = since
        self.counts = Counter()
        self.warnings = []
        self.now = timezone.now()
        # show media_id -> AniList statuses of its entries, for the show status
        self.show_statuses = defaultdict(set)
        self.bulk_dates = set()
        self.latest = None

    def run(self):
        """Import everything; return (counts, warnings, latest updatedAt)."""
        mappings.ensure_loaded()
        anime, manga = fetch_lists(self.username)
        shared = Counter(
            _datekey(entry["completedAt"])
            for entry in anime
            if entry["completedAt"] and entry["completedAt"].get("year")
        )
        self.bulk_dates = {key for key, n in shared.items() if n > BULK_DATE_LIMIT}

        fallback = AniListImporter(None, self.user, "new", self.username)
        for entry in anime:
            if not self._is_new(entry):
                continue
            try:
                if not self._import_anime(entry):
                    fallback._process_entry(entry, MediaTypes.ANIME.value)
                    self.counts["anime (unmapped)"] += 1
            except Exception as exc:  # one bad entry mustn't stop the import
                title = entry["media"]["title"]["userPreferred"]
                logger.warning("AniList import failed for %s", title, exc_info=True)
                self.warnings.append(f"{title}: {exc}")

        for entry in manga:
            if self._is_new(entry):
                fallback._process_entry(entry, MediaTypes.MANGA.value)
                self.counts["manga"] += 1

        helpers.bulk_create_media(fallback.bulk_media, self.user)
        helpers.bulk_update_media(
            fallback.to_update,
            helpers.EXISTING_MEDIA_UPDATE_FIELDS,
            self.user,
        )
        self._update_show_statuses()
        self.warnings.extend(fallback.warnings)
        return self.counts, self.warnings, self.latest

    def _is_new(self, entry):
        updated = entry.get("updatedAt") or 0
        if updated:
            stamp = datetime.datetime.fromtimestamp(updated, tz=datetime.UTC)
            if self.latest is None or stamp > self.latest:
                self.latest = stamp
            if self.since and stamp <= self.since:
                return False
        return True

    # Anime

    def _mapping(self, media):
        mapping = AnimeMapping.objects.filter(anilist_id=media["id"]).first()
        if mapping is None and media.get("idMal"):
            mapping = AnimeMapping.objects.filter(mal_id=media["idMal"]).first()
        return mapping

    def _import_anime(self, entry):
        """Import one anime entry as a show or movie; False if it can't be placed."""
        media = entry["media"]
        mapping = self._mapping(media)
        if mapping is None:
            return False
        if media["format"] == "MOVIE" and mapping.tmdb_movie:
            self._import_movie(entry, mapping.tmdb_movie)
            return True
        if mapping.tmdb_tv and self._import_show(entry, mapping):
            return True
        if mapping.tmdb_movie:
            self._import_movie(entry, mapping.tmdb_movie)
            return True
        return False

    @staticmethod
    def _start(mapping, media):
        """TMDB (season, offset) where the entry starts, or None if unknown.

        The mapping leaves the season out for long runners that are a single
        AniList entry (Naruto, Hunter x Hunter); those start at season 1.
        """
        if mapping.tmdb_season is not None:
            return mapping.tmdb_season, mapping.tmdb_offset
        if media["format"] in {"TV", "TV_SHORT", "ONA"}:
            return 1, 0
        return None

    def _watched_count(self, entry):
        progress = entry["progress"] or 0
        if entry["status"] == "COMPLETED":
            progress = max(progress, entry["media"]["episodes"] or 0)
        return progress

    def _real_completion(self, entry):
        """Return the AniList completion date, unless it is a list fill-in date."""
        completed = entry.get("completedAt") or {}
        if not completed.get("year") or _datekey(completed) in self.bulk_dates:
            return None
        return _date(completed)

    def _watch_date(self, entry, air_date):
        """When an episode was watched, following the rules in the module doc."""
        if entry["status"] != "COMPLETED":
            updated = entry.get("updatedAt")
            if updated:
                return datetime.datetime.fromtimestamp(updated, tz=datetime.UTC)
            return self.now
        real = self._real_completion(entry)
        media = entry["media"]
        started = (media.get("startDate") or {}).get("year")
        if real:
            day = real
        elif started and started >= AIRED_SINCE_YEAR:
            day = air_date or _date(media.get("endDate")) or OLD_SERIES_DATE
        else:
            day = OLD_SERIES_DATE
        return min(_at_noon(day), self.now)

    @staticmethod
    def _episode_slots(start, seasons, count, claimed=frozenset()):
        """(season, episode) pairs for `count` episodes from `start`.

        `start` is (season, offset) and `seasons` maps TMDB season numbers to
        their episode counts. Long runners are one AniList entry but several
        TMDB seasons, so episodes past the end of a season continue in the
        next one, unless another AniList entry is mapped to that season
        (`claimed`). Specials (season 0) never spill over.
        """
        season, offset = start
        slots = []
        episode = offset + 1
        if season == 0:
            order = [0]
        else:
            later = sorted(n for n in seasons if n > season)
            order = [season]
            for number in later:
                if number in claimed:
                    break
                order.append(number)
        for number in order:
            length = seasons.get(number) or 0
            while len(slots) < count and (not length or episode <= length):
                slots.append((number, episode))
                episode += 1
            if len(slots) >= count:
                break
            episode = 1
        return slots

    def _import_show(self, entry, mapping):
        """Mark the entry's episodes in its TMDB show; False if TMDB lacks the season.

        The mapping sometimes names a season TMDB has since merged or
        renumbered; such entries are better kept as plain anime than misplaced.
        """
        start = self._start(mapping, entry["media"])
        if start is None:
            return False
        media_id = str(mapping.tmdb_tv)
        tv_meta = app.providers.tmdb.tv(media_id)
        seasons = {
            season["season_number"]: season.get("max_progress") or 0
            for season in tv_meta.get("related", {}).get("seasons", [])
        }
        title = entry["media"]["title"]["userPreferred"]
        if start[0] not in seasons:
            logger.info("TMDB %s has no season %s for %s", media_id, start[0], title)
            return False
        claimed = set(
            AnimeMapping.objects.filter(tmdb_tv=mapping.tmdb_tv)
            .exclude(pk=mapping.pk)
            .exclude(tmdb_season=None)
            .values_list("tmdb_season", flat=True),
        )
        count = self._watched_count(entry)
        slots = self._episode_slots(start, seasons, count, claimed - {start[0]})
        if count and not slots:
            logger.info("No TMDB episodes in %s for %s", media_id, title)
            return False
        self.show_statuses[media_id].add(entry["status"])

        tv_item, _ = Item.objects.get_or_create(
            media_id=media_id,
            source=Sources.TMDB.value,
            media_type=MediaTypes.TV.value,
            defaults={"title": tv_meta["title"], "image": tv_meta["image"]},
        )
        tv, _ = TV.objects.get_or_create(
            item=tv_item,
            user=self.user,
            defaults={"status": Status.PLANNING.value},
        )
        if not slots:
            return True

        by_season = defaultdict(list)
        for season_number, episode_number in slots:
            by_season[season_number].append(episode_number)
        with_seasons = app.providers.tmdb.tv_with_seasons(media_id, list(by_season))

        for season_number, numbers in by_season.items():
            season_meta = with_seasons.get(f"season/{season_number}")
            if not season_meta:
                self.warnings.append(
                    f"{tv_meta['title']}: TMDB has no season {season_number}"
                )
                continue
            self._mark_episodes(entry, tv, tv_meta, season_meta, numbers)
        return True

    def _mark_episodes(self, entry, tv, tv_meta, season_meta, numbers):
        season_number = season_meta["season_number"]
        season_item, _ = Item.objects.get_or_create(
            media_id=tv.item.media_id,
            source=Sources.TMDB.value,
            media_type=MediaTypes.SEASON.value,
            season_number=season_number,
            defaults={"title": tv_meta["title"], "image": season_meta["image"]},
        )
        season, _ = Season.objects.get_or_create(
            item=season_item,
            user=self.user,
            related_tv=tv,
            defaults={"status": Status.PLANNING.value},
        )
        air_dates = {
            episode["episode_number"]: parse_date(episode.get("air_date") or "")
            for episode in season_meta["episodes"]
        }
        watched = set(
            season.episodes.values_list("item__episode_number", flat=True),
        )
        new_episodes = []
        for number in numbers:
            if number in watched or number not in air_dates:
                continue
            new_episodes.append(
                Episode(
                    related_season=season,
                    item=season.get_episode_item(number, season_meta),
                    end_date=self._watch_date(entry, air_dates[number]),
                ),
            )
            watched.add(number)
        bulk_create_with_history(new_episodes, Episode)
        self.counts["episodes"] += len(new_episodes)

        released = {
            number
            for number, aired in air_dates.items()
            if aired and aired <= self.now.date()
        }
        if released and released <= watched:
            status = Status.COMPLETED.value
        elif watched:
            status = Status.IN_PROGRESS.value
        else:
            status = Status.PLANNING.value
        self._advance(Season, season, status)

    def _import_movie(self, entry, tmdb_id):
        media_id = str(tmdb_id)
        meta = app.providers.tmdb.movie(media_id)
        item, _ = Item.objects.get_or_create(
            media_id=media_id,
            source=Sources.TMDB.value,
            media_type=MediaTypes.MOVIE.value,
            defaults={"title": meta["title"], "image": meta["image"]},
        )
        done = entry["status"] == "COMPLETED"
        status = Status.COMPLETED.value if done else Status.PLANNING.value
        if entry["status"] in WATCHING:
            status = Status.IN_PROGRESS.value
        movie = Movie.objects.filter(item=item, user=self.user).first()
        if movie is None:
            end = self._watch_date(entry, _date(entry["media"].get("startDate")))
            Movie.objects.create(
                item=item,
                user=self.user,
                status=status,
                progress=1 if done else 0,
                end_date=end if done else None,
            )
            self.counts["movies"] += 1
        elif helpers.advance_status(movie, status):
            Movie.objects.filter(pk=movie.pk).update(
                status=movie.status,
                progress=max(movie.progress or 0, 1 if done else 0),
            )

    @staticmethod
    def _advance(model, instance, status):
        """Move a status forward without the save hooks that add episodes."""
        if helpers.advance_status(instance, status):
            model.objects.filter(pk=instance.pk).update(status=instance.status)

    def _update_show_statuses(self):
        """Set each touched show's status from its seasons and AniList entries."""
        for media_id, statuses in self.show_statuses.items():
            tv = (
                TV.objects.filter(
                    item__media_id=media_id,
                    item__source=Sources.TMDB.value,
                    user=self.user,
                )
                .prefetch_related("seasons__item")
                .first()
            )
            if tv is None:
                continue
            regular = [s for s in tv.seasons.all() if s.item.season_number > 0]
            all_seasons = [
                season
                for season in app.providers.tmdb.tv(media_id)
                .get("related", {})
                .get("seasons", [])
                if season["season_number"] > 0
                and season.get("first_air_date")
                and str(season["first_air_date"]) <= self.now.date().isoformat()
            ]
            finished = (
                regular
                and len(regular) >= len(all_seasons)
                and all(s.status == Status.COMPLETED.value for s in regular)
            )
            if statuses & WATCHING:
                status = Status.IN_PROGRESS.value
            elif finished:
                status = Status.COMPLETED.value
            elif any(s.status != Status.PLANNING.value for s in tv.seasons.all()):
                status = Status.IN_PROGRESS.value
            else:
                status = Status.PLANNING.value
            if tv.status == Status.PLANNING.value and statuses <= {"PAUSED", "DROPPED"}:
                status = statuses.pop().capitalize()
                TV.objects.filter(pk=tv.pk).update(status=status)
                continue
            self._advance(TV, tv, status)
            self.counts["shows"] += 1
