from django.apps import AppConfig


class CatalogConfig(AppConfig):
    """Cross-provider catalog data: ID mappings, anime episodes, cast."""

    default_auto_field = "django.db.models.BigAutoField"
    name = "catalog"
