from django.urls import path

from reading import kosync

urlpatterns = [
    path("kosync/users/create", kosync.create_user, name="kosync_create_user"),
    path("kosync/users/auth", kosync.auth_user, name="kosync_auth_user"),
    path("kosync/syncs/progress", kosync.update_progress, name="kosync_update"),
    path(
        "kosync/syncs/progress/<str:document>",
        kosync.get_progress,
        name="kosync_get",
    ),
    path("kosync/healthcheck", kosync.healthcheck, name="kosync_healthcheck"),
]
