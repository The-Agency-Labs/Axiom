"""API token management (browser session only)."""

from django.shortcuts import get_object_or_404
from django.utils import timezone
from ninja import Router, Schema

from api.auth import require_session
from api.models import ApiToken, TokenScope

router = Router(tags=["tokens"])


def _token_dict(token):
    return {
        "id": token.id,
        "name": token.name,
        "scope": token.scope,
        "prefix": token.prefix,
        "created_at": token.created_at,
        "last_used_at": token.last_used_at,
        "revoked": token.revoked_at is not None,
    }


@router.get("/tokens")
def list_tokens(request):
    """List the user's API tokens (never the keys)."""
    require_session(request)
    return [_token_dict(t) for t in request.user.api_tokens.all()]


class TokenIn(Schema):
    """A new token's label and scope."""

    name: str
    scope: TokenScope = TokenScope.READ


@router.post("/tokens")
def create_token(request, payload: TokenIn):
    """Create a token. The key is returned once and cannot be shown again."""
    require_session(request)
    token, raw_key = ApiToken.objects.create_token(
        request.user,
        payload.name,
        payload.scope,
    )
    return {**_token_dict(token), "key": raw_key}


@router.delete("/tokens/{token_id}")
def revoke_token(request, token_id: int):
    """Revoke a token; devices using it stop syncing immediately."""
    require_session(request)
    token = get_object_or_404(ApiToken, id=token_id, user=request.user)
    token.revoked_at = timezone.now()
    token.save(update_fields=["revoked_at"])
    return _token_dict(token)
