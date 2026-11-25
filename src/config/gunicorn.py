import gc
import os

bind = "localhost:23847"
# One process with a few threads keeps memory low; provider calls are I/O bound.
workers = int(os.environ.get("GUNICORN_WORKERS", "1"))
threads = int(os.environ.get("GUNICORN_THREADS", "4"))
# Preloading only pays off when several workers share the app's memory. With a
# single worker it leaves a full copy of the app in the master process too.
preload_app = workers > 1
timeout = 200
max_requests = 500
max_requests_jitter = 10

accesslog = "-"
errorlog = "-"


def pre_fork(server, worker):  # noqa: ARG001
    """Freeze preloaded objects so forked workers keep sharing their memory."""
    gc.freeze()
