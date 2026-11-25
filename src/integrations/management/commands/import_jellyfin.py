"""Import Jellyfin watch history into Encompass (additive only)."""

from django.contrib.auth import get_user_model
from django.core.management.base import BaseCommand, CommandError

from integrations import tasks
from integrations.imports.jellyfin import JellyfinError


class Command(BaseCommand):
    """Replay every played movie and episode from Jellyfin."""

    help = __doc__

    def add_arguments(self, parser):
        """Encompass and Jellyfin usernames, and full vs catch-up."""
        parser.add_argument("username", help="Encompass username")
        parser.add_argument(
            "--jellyfin-user", help="Jellyfin username (defaults to the same name)"
        )
        parser.add_argument(
            "--since-last",
            action="store_true",
            help="Only plays since the last sync instead of the whole history",
        )

    def handle(self, *_args, **options):
        """Run the import and print a summary."""
        user = get_user_model().objects.filter(username=options["username"]).first()
        if user is None:
            msg = f"No Encompass user {options['username']!r}"
            raise CommandError(msg)
        try:
            counts, failures = tasks.run_jellyfin_import(
                user,
                options["jellyfin_user"] or user.username,
                full=not options["since_last"],
            )
        except JellyfinError as exc:
            raise CommandError(str(exc)) from exc
        self.stdout.write(
            f"Processed {counts.get('Movie', 0)} movies and "
            f"{counts.get('Episode', 0)} episodes; {len(failures)} failed."
        )
        for failure in failures:
            self.stdout.write(f"  failed: {failure}")
