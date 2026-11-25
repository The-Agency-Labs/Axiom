import datetime
from types import SimpleNamespace

from django.contrib.auth import get_user_model
from django.test import TestCase
from django.utils import timezone

from app.models import TV, Item, Season, Status
from integrations.imports.anilist_shows import OLD_SERIES_DATE, AniListShowImporter


def _entry(status="COMPLETED", start=2020, completed=None, end=None, updated=0):
    return {
        "status": status,
        "progress": 0,
        "updatedAt": updated,
        "completedAt": completed or {"year": None},
        "media": {
            "episodes": 12,
            "startDate": {"year": start, "month": 4, "day": 1},
            "endDate": end or {"year": start, "month": 6, "day": 20},
        },
    }


class EpisodeSlotTests(TestCase):
    """AniList entries map onto TMDB season and episode numbers."""

    def setUp(self):
        """Create an importer."""
        user = get_user_model().objects.create_user(username="al")
        self.importer = AniListShowImporter(user, "someone")

    def test_part_two_starts_after_offset(self):
        """A second cour inside one TMDB season starts after the offset."""
        slots = self.importer._episode_slots((3, 12), {1: 25, 2: 12, 3: 24}, 12)
        self.assertEqual(slots[0], (3, 13))
        self.assertEqual(slots[-1], (3, 24))

    def test_long_runner_spills_into_next_seasons(self):
        """One 60 episode entry fills TMDB seasons of 25, 25 and 10+."""
        slots = self.importer._episode_slots((1, 0), {0: 4, 1: 25, 2: 25, 3: 30}, 60)
        self.assertEqual(len(slots), 60)
        self.assertEqual(slots[25], (2, 1))
        self.assertEqual(slots[-1], (3, 10))

    def test_specials_never_spill(self):
        """Season 0 entries stay in season 0."""
        slots = self.importer._episode_slots((0, 2), {0: 3, 1: 12}, 4)
        self.assertEqual(slots, [(0, 3)])

    def test_spill_stops_at_a_season_of_another_entry(self):
        """A 13 episode first cour doesn't run into a separately listed S2."""
        slots = self.importer._episode_slots((1, 0), {1: 12, 2: 13}, 13, {2})
        self.assertEqual(len(slots), 12)

    def test_long_runner_without_season_starts_at_one(self):
        """Mapping rows without a season are single-entry long runners."""
        mapping = SimpleNamespace(tmdb_season=None, tmdb_offset=0)
        self.assertEqual(self.importer._start(mapping, {"format": "TV"}), (1, 0))
        self.assertIsNone(self.importer._start(mapping, {"format": "MOVIE"}))


class WatchDateTests(TestCase):
    """Watch dates follow the AniList date rules."""

    def setUp(self):
        """Create an importer."""
        user = get_user_model().objects.create_user(username="al")
        self.importer = AniListShowImporter(user, "someone")

    def test_recent_series_uses_air_date(self):
        """Series from 2012 on were watched as they aired."""
        when = self.importer._watch_date(_entry(start=2016), datetime.date(2016, 4, 8))
        self.assertEqual(when.date(), datetime.date(2016, 4, 8))

    def test_recent_series_without_air_date_uses_series_end(self):
        """Without an episode air date, the series end date is used."""
        when = self.importer._watch_date(_entry(start=2016), None)
        self.assertEqual(when.date(), datetime.date(2016, 6, 20))

    def test_old_series_gets_2019(self):
        """Series older than 2012 get the 2019 default."""
        when = self.importer._watch_date(_entry(start=2004), datetime.date(2004, 5, 1))
        self.assertEqual(when.date(), OLD_SERIES_DATE)

    def test_real_completion_date_is_kept(self):
        """A completion date specific to the entry is a real watch date."""
        entry = _entry(start=2004, completed={"year": 2023, "month": 7, "day": 4})
        when = self.importer._watch_date(entry, datetime.date(2004, 5, 1))
        self.assertEqual(when.date(), datetime.date(2023, 7, 4))

    def test_bulk_completion_date_is_ignored(self):
        """A completion date shared by many entries is a fill-in date."""
        self.importer.bulk_dates = {(2020, 2, 6)}
        entry = _entry(start=2016, completed={"year": 2020, "month": 2, "day": 6})
        when = self.importer._watch_date(entry, datetime.date(2016, 4, 8))
        self.assertEqual(when.date(), datetime.date(2016, 4, 8))

    def test_watching_uses_last_update(self):
        """Episodes of a show being watched are dated by the AniList update."""
        stamp = int(datetime.datetime(2026, 11, 1, tzinfo=datetime.UTC).timestamp())
        entry = _entry(status="CURRENT", updated=stamp)
        when = self.importer._watch_date(entry, datetime.date(2016, 4, 8))
        self.assertEqual(when, datetime.datetime(2026, 11, 1, tzinfo=datetime.UTC))

    def test_never_in_the_future(self):
        """An episode that airs later is dated now at the latest."""
        future = timezone.localdate() + datetime.timedelta(days=30)
        when = self.importer._watch_date(_entry(start=2026), future)
        self.assertLessEqual(when, timezone.now())


class ContinuousNumberingTests(TestCase):
    """Seasons numbered on from the previous one are marked by position."""

    def test_second_season_numbered_from_33(self):
        """Position 1 of a season whose episodes are 33..35 marks episode 33."""
        user = get_user_model().objects.create_user(username="al")
        importer = AniListShowImporter(user, "someone")
        tv_item = Item.objects.create(
            media_id="31910", source="tmdb", media_type="tv", title="Show"
        )
        tv = TV.objects.create(item=tv_item, user=user, status=Status.PLANNING.value)
        season_meta = {
            "season_number": 2,
            "image": "",
            "episodes": [
                {"episode_number": n, "air_date": "2008-01-01"} for n in (33, 34, 35)
            ],
        }
        entry = _entry(start=2007)
        importer._mark_episodes(entry, tv, {"title": "Show"}, season_meta, [1, 2])
        season = Season.objects.get(user=user, item__season_number=2)
        self.assertEqual(
            sorted(season.episodes.values_list("item__episode_number", flat=True)),
            [33, 34],
        )
        self.assertEqual(season.status, Status.IN_PROGRESS.value)
