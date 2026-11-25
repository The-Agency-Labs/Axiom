import os

from celery import Celery, bootsteps
from celery.beat import EmbeddedService

# Set the default Django settings module for the 'celery' program.
os.environ.setdefault("DJANGO_SETTINGS_MODULE", "config.settings")

app = Celery("yamtrack")

app.config_from_object("django.conf:settings", namespace="CELERY")

# Load task modules from all registered Django apps.
app.autodiscover_tasks()


class BeatThread(bootsteps.StartStopStep):
    """Run the beat scheduler as a thread inside the worker.

    `celery worker --beat` forks a second process holding another full copy of
    Django just to send a few periodic tasks; a thread costs almost nothing.
    """

    def __init__(self, worker, **kwargs):
        """Nothing to set up until the worker starts."""
        super().__init__(worker, **kwargs)
        self.service = None

    def start(self, worker):
        """Start the scheduler thread."""
        self.service = EmbeddedService(worker.app, thread=True)
        self.service.start()

    def stop(self, worker):  # noqa: ARG002
        """Stop the scheduler thread."""
        if self.service:
            self.service.stop()


if os.environ.get("CELERY_BEAT_THREAD", "True").lower() != "false":
    app.steps["worker"].add(BeatThread)
