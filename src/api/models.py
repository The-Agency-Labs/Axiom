import hashlib
import secrets

from django.conf import settings
from django.db import models
from django.utils import timezone

TOKEN_PREFIX = "enc_"  # noqa: S105 public prefix, not a secret


def sha256_hex(value):
    """Return the SHA-256 hex digest of a string."""
    return hashlib.sha256(value.encode()).hexdigest()


def md5_hex(value):
    """Return the MD5 hex digest of a string (KOReader hashes passwords this way)."""
    return hashlib.md5(value.encode()).hexdigest()  # noqa: S324


class TokenScope(models.TextChoices):
    """What a token is allowed to do."""

    READ = "read", "Read only"
    WRITE = "write", "Read and write"


class ApiTokenManager(models.Manager):
    """Create and look up API tokens without ever storing the raw key."""

    def create_token(self, user, name, scope=TokenScope.READ):
        """Create a token and return ``(token, raw_key)``; the key is shown once."""
        raw_key = TOKEN_PREFIX + secrets.token_urlsafe(32)
        token = self.create(
            user=user,
            name=name,
            scope=scope,
            prefix=raw_key[: len(TOKEN_PREFIX) + 6],
            key_hash=sha256_hex(raw_key),
            key_md5=md5_hex(raw_key),
        )
        return token, raw_key

    def authenticate(self, raw_key):
        """Return the active token for a raw key, or ``None``."""
        if not raw_key:
            return None
        return self._touch(
            self.select_related("user")
            .filter(key_hash=sha256_hex(raw_key), revoked_at__isnull=True)
            .first(),
        )

    def authenticate_md5(self, username, key_md5):
        """Return the active token for a KOSync ``x-auth-user``/``x-auth-key`` pair."""
        if not username or not key_md5:
            return None
        return self._touch(
            self.select_related("user")
            .filter(
                user__username=username,
                key_md5=key_md5.lower(),
                revoked_at__isnull=True,
            )
            .first(),
        )

    def _touch(self, token):
        if token is not None and token.user.is_active:
            now = timezone.now()
            # Avoid a write on every request: refresh at most once a minute.
            stale = (
                not token.last_used_at
                or (now - token.last_used_at).total_seconds() > 60  # noqa: PLR2004
            )
            if stale:
                self.filter(pk=token.pk).update(last_used_at=now)
            return token
        return None


class ApiToken(models.Model):
    """A named, revocable credential for devices and assistants.

    Only hashes are stored. ``key_md5`` exists because the KOSync protocol sends
    ``md5(password)``; the key is 32 random bytes, so the MD5 cannot be reversed.
    """

    user = models.ForeignKey(
        settings.AUTH_USER_MODEL,
        on_delete=models.CASCADE,
        related_name="api_tokens",
    )
    name = models.CharField(max_length=100)
    scope = models.CharField(
        max_length=10,
        choices=TokenScope.choices,
        default=TokenScope.READ,
    )
    prefix = models.CharField(max_length=16)
    key_hash = models.CharField(max_length=64, unique=True)
    key_md5 = models.CharField(max_length=32, db_index=True)
    created_at = models.DateTimeField(auto_now_add=True)
    last_used_at = models.DateTimeField(null=True, blank=True)
    revoked_at = models.DateTimeField(null=True, blank=True)

    objects = ApiTokenManager()

    class Meta:
        """Model options."""

        ordering = ["-created_at"]

    def __str__(self):
        """Return a readable label."""
        return f"{self.name} ({self.prefix}…)"

    @property
    def can_write(self):
        """Whether the token may modify data."""
        return self.scope == TokenScope.WRITE
