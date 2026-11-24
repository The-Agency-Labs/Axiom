from django.contrib.auth import get_user_model
from django.core.management.base import BaseCommand, CommandError

from api.models import ApiToken, TokenScope


class Command(BaseCommand):
    """Create an API token from the command line (prints the key once)."""

    help = "Create an API token for a user, e.g. for KOReader or an assistant."

    def add_arguments(self, parser):
        """Define CLI arguments."""
        parser.add_argument("username")
        parser.add_argument("name", help="Label, e.g. 'kindle' or 'assistant'")
        parser.add_argument(
            "--scope",
            choices=TokenScope.values,
            default=TokenScope.READ,
        )

    def handle(self, *args, **options):  # noqa: ARG002
        """Create the token and print its key."""
        try:
            user = get_user_model().objects.get(username=options["username"])
        except get_user_model().DoesNotExist as exc:
            msg = f"No user named {options['username']!r}"
            raise CommandError(msg) from exc
        token, key = ApiToken.objects.create_token(
            user,
            options["name"],
            options["scope"],
        )
        self.stdout.write(f"{token.name} ({token.scope}): {key}")
