from django.core.management.base import BaseCommand

from catalog import credits  # noqa: A004
from catalog.tasks import library_items


class Command(BaseCommand):
    """Fetch cast for every show, movie and anime in the library."""

    help = "Fetch cast for library items (missing only, or all with --force)."

    def add_arguments(self, parser):
        """Add options."""
        parser.add_argument("--force", action="store_true", help="Refetch everything")

    def handle(self, *args, **options):  # noqa: ARG002
        """Fetch credits."""
        items = list(library_items())
        if not options["force"]:
            items = credits.missing_items(items)
        for item in items:
            credits.fetch(item, force=options["force"])
            self.stdout.write(f"  {item}")
        self.stdout.write(self.style.SUCCESS(f"Fetched cast for {len(items)} items"))
