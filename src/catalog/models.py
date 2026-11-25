"""Shared catalog data that isn't per user: ID mappings and cast credits."""

from django.db import models


class AnimeMapping(models.Model):
    """One row of the Fribb anime-lists dataset (MAL/AniList -> TMDB/TVDB).

    ``tmdb_season`` and ``tmdb_offset`` place a MAL/AniList entry (often one
    cour) inside a TMDB show, e.g. a "Part 2" is season 3 starting after
    episode 12.
    """

    mal_id = models.PositiveIntegerField(null=True, db_index=True)
    anilist_id = models.PositiveIntegerField(null=True, db_index=True)
    kind = models.CharField(max_length=20, blank=True)
    tmdb_tv = models.PositiveIntegerField(null=True)
    tmdb_movie = models.PositiveIntegerField(null=True)
    tmdb_season = models.IntegerField(null=True)
    tmdb_offset = models.IntegerField(default=0)
    tvdb_id = models.PositiveIntegerField(null=True)

    def __str__(self):
        """Describe the mapping."""
        return f"MAL {self.mal_id} / AniList {self.anilist_id} -> TMDB {self.tmdb_tv}"


class Person(models.Model):
    """An actor, voice actor or crew member from one provider."""

    source = models.CharField(max_length=20)
    external_id = models.CharField(max_length=40)
    name = models.CharField(max_length=255)
    native_name = models.CharField(max_length=255, blank=True)
    image = models.URLField(max_length=500, blank=True)

    class Meta:
        """Meta."""

        constraints = [
            models.UniqueConstraint(
                fields=["source", "external_id"],
                name="unique_person_per_source",
            ),
        ]

    def __str__(self):
        """Return the name."""
        return self.name


class CreditRole(models.TextChoices):
    """How a person is credited."""

    ACTOR = "actor", "Actor"
    VOICE = "voice", "Voice actor"
    CREW = "crew", "Crew"


class Credit(models.Model):
    """A person credited on an item (a show, movie or anime)."""

    item = models.ForeignKey(
        "app.Item", on_delete=models.CASCADE, related_name="credits"
    )
    person = models.ForeignKey(Person, on_delete=models.CASCADE, related_name="credits")
    role = models.CharField(max_length=10, choices=CreditRole.choices)
    character = models.CharField(max_length=255, blank=True)
    character_image = models.URLField(max_length=500, blank=True)
    character_role = models.CharField(max_length=20, blank=True)  # MAIN/SUPPORTING
    language = models.CharField(max_length=20, blank=True)  # voice actors
    job = models.CharField(max_length=100, blank=True)  # crew
    order = models.PositiveIntegerField(default=0)
    episode_count = models.PositiveIntegerField(null=True)

    class Meta:
        """Meta."""

        ordering = ["order"]
        indexes = [models.Index(fields=["person", "item"])]

    def __str__(self):
        """Describe the credit."""
        return f"{self.person} as {self.character or self.job} in {self.item}"


class CreditFetch(models.Model):
    """When an item's credits were last fetched, so they refresh monthly."""

    item = models.OneToOneField(
        "app.Item", on_delete=models.CASCADE, related_name="credit_fetch"
    )
    fetched_at = models.DateTimeField(auto_now=True)
    count = models.PositiveIntegerField(default=0)
    error = models.CharField(max_length=255, blank=True)

    def __str__(self):
        """Describe the fetch."""
        return f"{self.item} ({self.count} credits)"
