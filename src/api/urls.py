"""Mount the JSON API at ``/api/v1/`` (interactive docs at ``/api/v1/docs``)."""

from django.urls import path
from ninja import NinjaAPI

from api import assistant, library, now, reading, session, tokens
from api.auth import AUTH

api = NinjaAPI(
    title="Encompass API",
    version="1",
    auth=AUTH,
    urls_namespace="api",
    description=(
        "Authenticate with `Authorization: Bearer <token>`. Read-only tokens "
        "can call every GET endpoint; write tokens can also change data."
    ),
)
api.add_router("", session.router)
api.add_router("", now.router)
api.add_router("", assistant.router)
api.add_router("", library.router)
api.add_router("", reading.router)
api.add_router("", tokens.router)


def _public(patterns):
    """Exempt API views from LoginRequiredMiddleware; ninja enforces auth itself."""
    for pattern in patterns:
        pattern.callback.login_required = False
    return patterns


_patterns, _app_name, _namespace = api.urls
urlpatterns = [path("api/v1/", (_public(_patterns), _app_name, _namespace))]
