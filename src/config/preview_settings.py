"""Local preview without Redis: in-process fake Redis, tasks run inline.

For trying the UI on a dev machine only, never for deployment:
    DJANGO_SETTINGS_MODULE=config.preview_settings python manage.py runserver
"""

from fakeredis import FakeRedisConnection

from .settings import *  # noqa: F403

DEBUG = True
TESTING = True  # makes the provider rate limiter use fakeredis

CACHES = {
    "default": {
        "BACKEND": "django_redis.cache.RedisCache",
        "LOCATION": REDIS_URL,  # noqa: F405
        "TIMEOUT": CACHE_TIMEOUT,  # noqa: F405
        "OPTIONS": {
            "CONNECTION_POOL_KWARGS": {"connection_class": FakeRedisConnection},
        },
    },
}

CELERY_TASK_ALWAYS_EAGER = True
CELERY_BROKER_URL = "memory://"
CSRF_TRUSTED_ORIGINS = ["http://localhost:5173", "http://127.0.0.1:5173"]
DEBUG_TOOLBAR_CONFIG = {"SHOW_TOOLBAR_CALLBACK": lambda _request: False}
