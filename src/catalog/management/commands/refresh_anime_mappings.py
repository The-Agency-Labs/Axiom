from django.core.management.base import BaseCommand

from catalog import mappings


class Command(BaseCommand):
    """Download the anime ID mapping dataset (MAL/AniList -> TMDB)."""

    help = "Download the anime ID mapping dataset."

    def handle(self, *args, **options):  # noqa: ARG002
        """Refresh the mappings."""
        count = mappings.refresh()
        self.stdout.write(self.style.SUCCESS(f"Loaded {count} anime mappings"))
