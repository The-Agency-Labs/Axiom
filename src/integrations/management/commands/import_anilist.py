"""Import an AniList list into Encompass, anime as whole shows (additive only)."""

from django.contrib.auth import get_user_model
from django.core.management.base import BaseCommand, CommandError
from django.db import transaction

from integrations import tasks
from integrations.imports.helpers import MediaImportError


class Command(BaseCommand):
    """Import a public AniList user's anime and manga."""

    help = __doc__

    def add_arguments(self, parser):
        """Encompass and AniList usernames, catch-up and dry run."""
        parser.add_argument("username", help="Encompass username")
        parser.add_argument("anilist_user", help="AniList username")
        parser.add_argument(
            "--since-last",
            action="store_true",
            help="Only entries edited since the last sync",
        )
        parser.add_argument(
            "--dry-run",
            action="store_true",
            help="Run the import and roll it back, to preview the counts",
        )

    def handle(self, *_args, **options):
        """Run the import and print a summary."""
        user = get_user_model().objects.filter(username=options["username"]).first()
        if user is None:
            msg = f"No Encompass user {options['username']!r}"
            raise CommandError(msg)
        try:
            with transaction.atomic():
                counts, warnings = tasks.run_anilist_import(
                    user,
                    options["anilist_user"],
                    full=not options["since_last"],
                    dry_run=options["dry_run"],
                )
                if options["dry_run"]:
                    transaction.set_rollback(True)
        except MediaImportError as exc:
            raise CommandError(str(exc)) from exc
        prefix = "Would import" if options["dry_run"] else "Imported"
        self.stdout.write(f"{prefix}: {dict(counts)}")
        for warning in warnings:
            self.stdout.write(f"  warning: {warning}")
