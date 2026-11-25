import gc
import os

bind = "localhost:23847"
preload_app = True
# One process with a few threads keeps memory low; provider calls are I/O bound.
workers = int(os.environ.get("GUNICORN_WORKERS", "1"))
threads = int(os.environ.get("GUNICORN_THREADS", "4"))
timeout = 200
max_requests = 500
max_requests_jitter = 10

accesslog = "-"
errorlog = "-"


def pre_fork(server, worker):  # noqa: ARG001
    """Freeze preloaded objects so the forked worker keeps sharing their memory."""
    gc.freeze()
