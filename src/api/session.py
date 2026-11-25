"""Browser login for the single-page frontend (session cookie + CSRF)."""

from django.contrib.auth import authenticate, login, logout
from django.middleware.csrf import get_token
from ninja import Router, Schema
from ninja.errors import HttpError
from ninja.utils import check_csrf

router = Router(tags=["session"])


@router.get("/auth/csrf", auth=None)
def csrf(request):
    """Set the CSRF cookie and return the token for the X-CSRFToken header."""
    return {
        "csrf": get_token(request),
        "authenticated": request.user.is_authenticated,
        "username": request.user.username if request.user.is_authenticated else None,
    }


class LoginIn(Schema):
    """Username and password."""

    username: str
    password: str


@router.post("/auth/login", auth=None)
def session_login(request, payload: LoginIn):
    """Log in and start a session."""
    error = check_csrf(request)
    if error:
        raise HttpError(403, "CSRF check failed")
    user = authenticate(request, username=payload.username, password=payload.password)
    if user is None:
        raise HttpError(401, "Invalid username or password")
    login(request, user, backend="django.contrib.auth.backends.ModelBackend")
    return {"username": user.username, "csrf": get_token(request)}


@router.post("/auth/logout")
def session_logout(request):
    """End the session."""
    logout(request)
    return {"ok": True}
