"""Fill a demo account with real titles so the UI has something to show.

Looks every title up through the configured providers, so it needs network.
"""

import datetime
import random

from django.contrib.auth import get_user_model
from django.core.management.base import BaseCommand
from django.utils import timezone

from app.models import (
    TV,
    Anime,
    Book,
    Episode,
    Item,
    Manga,
    MediaTypes,
    Movie,
    Season,
    Status,
)
from app.providers import services
from reading.models import ReadingDocument, ReadingSession, SessionSource

# (media_type, query, source, status, progress)
DEMO = [
    (MediaTypes.ANIME.value, "Frieren", "mal", Status.IN_PROGRESS, 17),
    (MediaTypes.ANIME.value, "Dandadan", "mal", Status.IN_PROGRESS, 6),
    (MediaTypes.ANIME.value, "Cowboy Bebop", "mal", Status.COMPLETED, None),
    (MediaTypes.MANGA.value, "Vagabond", "mal", Status.IN_PROGRESS, 214),
    (MediaTypes.MANGA.value, "Chainsaw Man", "mal", Status.IN_PROGRESS, 160),
    (MediaTypes.MANGA.value, "Berserk", "mal", Status.PLANNING, 0),
    (MediaTypes.BOOK.value, "Project Hail Mary", "openlibrary", Status.IN_PROGRESS, 0),
    (MediaTypes.BOOK.value, "Dune", "openlibrary", Status.COMPLETED, None),
    (MediaTypes.BOOK.value, "The Name of the Wind", "openlibrary", Status.PLANNING, 0),
    (MediaTypes.MOVIE.value, "Dune Part Two", "tmdb", Status.COMPLETED, None),
    (MediaTypes.MOVIE.value, "Perfect Days", "tmdb", Status.PLANNING, None),
    (MediaTypes.MOVIE.value, "Spirited Away", "tmdb", Status.COMPLETED, None),
]
# (query, season, episodes watched)
DEMO_TV = [("Severance", 2, 6), ("Shogun", 1, 10), ("The Bear", 3, 4)]

MODELS = {
    MediaTypes.ANIME.value: Anime,
    MediaTypes.MANGA.value: Manga,
    MediaTypes.BOOK.value: Book,
    MediaTypes.MOVIE.value: Movie,
}


class Command(BaseCommand):
    """Seed a demo user."""

    help = "Create a demo user with real titles, TV progress and reading sessions."

    def add_arguments(self, parser):
        """Define CLI arguments."""
        parser.add_argument("--username", default="demo")
        parser.add_argument("--password", default="demo-password")

    def _first(self, media_type, query, source=None):
        results = services.search(media_type, query, 1, source)
        results = results.get("results", results)
        if not results:
            return None
        return results[0]

    def _item(self, media_type, result, season_number=None):
        meta = services.get_media_metadata(
            media_type,
            str(result["media_id"]),
            result["source"],
            [season_number] if season_number is not None else None,
        )
        item, _ = Item.objects.get_or_create(
            media_id=str(result["media_id"]),
            source=result["source"],
            media_type=media_type,
            season_number=season_number,
            defaults={"title": meta["title"], "image": meta["image"]},
        )
        return item, meta

    def handle(self, *args, **options):  # noqa: ARG002
        """Create the user and data."""
        user_model = get_user_model()
        user, created = user_model.objects.get_or_create(username=options["username"])
        if created:
            user.set_password(options["password"])
            user.save()
        now = timezone.now()

        for offset, (media_type, query, source, status, progress) in enumerate(DEMO):
            result = self._first(media_type, query, source)
            if not result:
                self.stderr.write(f"No result for {query}")
                continue
            item, meta = self._item(media_type, result)
            model = MODELS[media_type]
            if model.objects.filter(user=user, item=item).exists():
                continue
            entry = model(user=user, item=item, status=status.value)
            if progress is not None and media_type != MediaTypes.MOVIE.value:
                entry.progress = progress
            entry.start_date = now - datetime.timedelta(days=30 + offset)
            if status == Status.COMPLETED:
                entry.end_date = now - datetime.timedelta(days=offset * 3)
                entry.score = random.choice([8, 8.5, 9, 9.5, 10])  # noqa: S311
            entry.save()
            self.stdout.write(f"{media_type}: {meta['title']}")

        for query, season_number, watched in DEMO_TV:
            result = self._first(MediaTypes.TV.value, query)
            if not result:
                continue
            tv_item, tv_meta = self._item(MediaTypes.TV.value, result)
            tv, _ = TV.objects.get_or_create(
                user=user,
                item=tv_item,
                defaults={"status": Status.IN_PROGRESS.value},
            )
            season_item, season_meta = self._item(
                MediaTypes.SEASON.value,
                result,
                season_number,
            )
            season, made = Season.objects.get_or_create(
                user=user,
                item=season_item,
                related_tv=tv,
                defaults={"status": Status.IN_PROGRESS.value},
            )
            if made:
                for number in range(1, watched + 1):
                    episode_item, _ = Item.objects.get_or_create(
                        media_id=season_item.media_id,
                        source=season_item.source,
                        media_type=MediaTypes.EPISODE.value,
                        season_number=season_number,
                        episode_number=number,
                        defaults={
                            "title": season_item.title,
                            "image": season_item.image,
                        },
                    )
                    Episode.objects.create(
                        item=episode_item,
                        related_season=season,
                        end_date=now - datetime.timedelta(days=(watched - number) * 2),
                    )
            self.stdout.write(f"tv: {tv_meta['title']} S{season_number}")

        self._reading(user, now)
        self.stdout.write(self.style.SUCCESS(f"Log in as {user.username}"))

    def _reading(self, user, now):
        book = (
            Book.objects.filter(user=user, status=Status.IN_PROGRESS.value)
            .select_related("item")
            .first()
        )
        if book is None or ReadingSession.objects.filter(user=user).exists():
            return
        document = ReadingDocument.objects.create(
            user=user,
            document_hash="demo" + book.item.media_id[:20],
            title=book.item.title,
            total_pages=480,
            item=book.item,
            percentage=0.42,
            device="Kindle",
        )
        rng = random.Random(7)
        page = 1
        for days_ago in range(45, -1, -1):
            if rng.random() < 0.22 and days_ago not in (0, 1, 2, 3):  # noqa: PLR2004
                continue
            start = (now - datetime.timedelta(days=days_ago)).replace(
                hour=rng.choice([7, 12, 21, 22]),
                minute=rng.randint(0, 59),
            )
            pages = rng.randint(6, 34)
            minutes = pages * rng.randint(1, 3)
            ReadingSession.objects.create(
                user=user,
                source=SessionSource.KOREADER,
                document=document,
                item=book.item,
                title=book.item.title,
                started_at=start,
                ended_at=start + datetime.timedelta(minutes=minutes),
                duration_seconds=minutes * 60,
                pages_read=pages,
                start_page=page,
                end_page=page + pages,
                device="Kindle",
            )
            page += pages
        book.progress = min(page, 200)
        book.save()
