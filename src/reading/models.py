from django.conf import settings
from django.db import models

from app.models import Item


class SessionSource(models.TextChoices):
    """Where a reading session came from."""

    KOREADER = "koreader", "KOReader"
    PAPERBACK = "paperback", "Paperback"
    MANUAL = "manual", "Manual"


class ReadingDocument(models.Model):
    """A file as a reading device knows it, optionally linked to a library item.

    KOReader identifies books by a partial MD5 of the file (``document_hash``),
    not by ISBN, so a document is matched to an ``Item`` once and then reused.
    """

    user = models.ForeignKey(
        settings.AUTH_USER_MODEL,
        on_delete=models.CASCADE,
        related_name="reading_documents",
    )
    document_hash = models.CharField(max_length=64)
    title = models.TextField(blank=True, default="")
    authors = models.TextField(blank=True, default="")
    total_pages = models.PositiveIntegerField(null=True, blank=True)
    item = models.ForeignKey(
        Item,
        on_delete=models.SET_NULL,
        null=True,
        blank=True,
        related_name="reading_documents",
    )

    # Last position reported through KOSync.
    progress = models.TextField(blank=True, default="")
    percentage = models.FloatField(null=True, blank=True)
    device = models.CharField(max_length=100, blank=True, default="")
    device_id = models.CharField(max_length=100, blank=True, default="")
    progress_updated_at = models.DateTimeField(null=True, blank=True)

    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)

    class Meta:
        """Model options."""

        constraints = [
            models.UniqueConstraint(
                fields=["user", "document_hash"],
                name="reading_document_unique_per_user",
            ),
        ]
        ordering = ["-updated_at"]

    def __str__(self):
        """Return a readable label."""
        return self.title or self.document_hash


class PageEvent(models.Model):
    """One page view reported by KOReader's statistics plugin.

    Raw events are kept so uploads are idempotent (the device may resend rows)
    and sessions can be rebuilt if the grouping rules change.
    """

    document = models.ForeignKey(
        ReadingDocument,
        on_delete=models.CASCADE,
        related_name="page_events",
    )
    page = models.PositiveIntegerField()
    start_time = models.DateTimeField()
    duration_seconds = models.PositiveIntegerField()
    total_pages = models.PositiveIntegerField(null=True, blank=True)

    class Meta:
        """Model options."""

        constraints = [
            models.UniqueConstraint(
                fields=["document", "page", "start_time"],
                name="reading_page_event_unique",
            ),
        ]
        ordering = ["start_time"]

    def __str__(self):
        """Return a readable label."""
        return f"{self.document} p{self.page} @ {self.start_time:%Y-%m-%d %H:%M}"


class ReadingSession(models.Model):
    """A continuous stretch of reading, from any source."""

    user = models.ForeignKey(
        settings.AUTH_USER_MODEL,
        on_delete=models.CASCADE,
        related_name="reading_sessions",
    )
    source = models.CharField(max_length=20, choices=SessionSource.choices)
    document = models.ForeignKey(
        ReadingDocument,
        on_delete=models.CASCADE,
        null=True,
        blank=True,
        related_name="sessions",
    )
    item = models.ForeignKey(
        Item,
        on_delete=models.SET_NULL,
        null=True,
        blank=True,
        related_name="reading_sessions",
    )
    title = models.TextField(blank=True, default="")
    started_at = models.DateTimeField(db_index=True)
    ended_at = models.DateTimeField()
    duration_seconds = models.PositiveIntegerField(default=0)
    pages_read = models.PositiveIntegerField(default=0)
    start_page = models.PositiveIntegerField(null=True, blank=True)
    end_page = models.PositiveIntegerField(null=True, blank=True)
    # Chapters for manga sessions (Paperback); pages for books.
    chapters_read = models.PositiveIntegerField(default=0)
    device = models.CharField(max_length=100, blank=True, default="")

    class Meta:
        """Model options."""

        ordering = ["-started_at"]
        indexes = [models.Index(fields=["user", "started_at"])]

    def __str__(self):
        """Return a readable label."""
        return f"{self.title} {self.started_at:%Y-%m-%d %H:%M} ({self.pages_read}p)"
