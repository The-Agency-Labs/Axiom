"""Authentication for the JSON API.

Two ways in:
* ``Authorization: Bearer <token>`` — devices, scripts and assistants.
* The web app's session cookie (with CSRF) — the browser frontend.

Session users get full access; tokens are limited by their scope.
"""

from ninja.errors import HttpError
from ninja.security import HttpBearer, django_auth

from api.models import ApiToken


class BearerToken(HttpBearer):
    """Authenticate with an API token and attach its user to the request."""

    def authenticate(self, request, token):
        """Return the token if valid; ninja treats a falsy result as 401."""
        api_token = ApiToken.objects.authenticate(token)
        if api_token is None:
            return None
        request.user = api_token.user
        request.api_token = api_token
        return api_token


bearer = BearerToken()
AUTH = [bearer, django_auth]


def require_write(request):
    """Raise 403 if the request uses a read-only token."""
    token = getattr(request, "api_token", None)
    if token is not None and not token.can_write:
        raise HttpError(403, "This token is read-only")


def require_session(request):
    """Raise 403 unless the request comes from a logged-in browser session."""
    if getattr(request, "api_token", None) is not None:
        raise HttpError(403, "Manage tokens from the web app")
