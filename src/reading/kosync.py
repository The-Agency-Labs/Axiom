"""KOReader sync server (KOSync protocol).

Point KOReader's "Progress sync" at ``https://<host>/kosync`` and log in with
your username and an API token as the password. Registration through KOReader
is refused: tokens are created in the web app.

Protocol reference: https://github.com/koreader/koreader-sync-server
"""

import json
import logging

from django.contrib.auth.decorators import login_not_required
from django.http import JsonResponse
from django.views.decorators.csrf import csrf_exempt
from django.views.decorators.http import require_GET, require_http_methods

from api.models import ApiToken
from reading import services
from reading.models import ReadingDocument

logger = logging.getLogger(__name__)

KOSYNC_CONTENT_TYPE = "application/vnd.koreader.v1+json"


def _json(data, status=200):
    return JsonResponse(data, status=status, content_type=KOSYNC_CONTENT_TYPE)


def _unauthorized():
    return _json({"code": 2001, "message": "Unauthorized"}, status=401)


def _authenticate(request):
    return ApiToken.objects.authenticate_md5(
        request.headers.get("x-auth-user"),
        request.headers.get("x-auth-key"),
    )


@login_not_required
@csrf_exempt
@require_http_methods(["POST"])
def create_user(_request):
    """Refuse device-side registration."""
    return _json(
        {
            "code": 2002,
            "message": "Registration is disabled. Create an API token in the web "
            "app and log in with it as the password.",
        },
        status=402,
    )


@login_not_required
@require_GET
def auth_user(request):
    """Check credentials (KOReader's "Login")."""
    if _authenticate(request) is None:
        return _unauthorized()
    return _json({"authorized": "OK"})


@login_not_required
@csrf_exempt
@require_http_methods(["PUT"])
def update_progress(request):
    """Store the reading position for a document."""
    token = _authenticate(request)
    if token is None:
        return _unauthorized()
    if not token.can_write:
        return _json({"code": 2001, "message": "Token is read-only"}, status=403)

    try:
        payload = json.loads(request.body or b"{}")
        document_hash = str(payload["document"])
        percentage = float(payload.get("percentage", 0))
    except (ValueError, KeyError, TypeError):
        return _json({"code": 2003, "message": "Invalid request"}, status=403)

    document = services.get_or_create_document(token.user, document_hash)
    services.update_document_progress(
        document,
        progress=str(payload.get("progress", "")),
        percentage=max(0.0, min(percentage, 1.0)),
        device=str(payload.get("device", "")),
        device_id=str(payload.get("device_id", "")),
    )
    return _json(
        {
            "document": document.document_hash,
            "timestamp": int(document.progress_updated_at.timestamp()),
        },
    )


@login_not_required
@require_GET
def get_progress(request, document):
    """Return the stored position for a document."""
    token = _authenticate(request)
    if token is None:
        return _unauthorized()
    doc = ReadingDocument.objects.filter(
        user=token.user,
        document_hash=document.lower(),
        progress_updated_at__isnull=False,
    ).first()
    if doc is None:
        return _json({})
    return _json(
        {
            "document": doc.document_hash,
            "progress": doc.progress,
            "percentage": doc.percentage,
            "device": doc.device,
            "device_id": doc.device_id,
            "timestamp": int(doc.progress_updated_at.timestamp()),
        },
    )


@login_not_required
@require_GET
def healthcheck(_request):
    """Liveness probe used by some KOSync clients."""
    return _json({"state": "OK"})
