from typing import Annotated, Any

from fastapi import APIRouter, Body

from app.deps import Kuma

router = APIRouter()

# Options depend on the notification type: the body is forwarded as-is and validated
# by uptime-kuma-api (invalid options return 422).
Options = Annotated[
    dict[str, Any],
    Body(
        examples=[
            {
                "name": "My alert",
                "type": "webhook",
                "webhookURL": "https://example.com/hook",
                "webhookContentType": "json",
            }
        ]
    ),
]


@router.get("", summary="Get all notifications")
def get_notifications(kuma: Kuma) -> dict[str, Any]:
    return {"notifications": kuma.get_notifications()}


@router.post("", summary="Add a notification")
def add_notification(options: Options, kuma: Kuma) -> dict[str, Any]:
    return kuma.add_notification(**options)


@router.post("/test", summary="Test a notification")
def test_notification(options: Options, kuma: Kuma) -> dict[str, Any]:
    return kuma.test_notification(**options)


@router.get("/{notification_id}", summary="Get a notification")
def get_notification(notification_id: int, kuma: Kuma) -> dict[str, Any]:
    return {"notification": kuma.get_notification(notification_id)}


@router.patch("/{notification_id}", summary="Update a notification")
def update_notification(notification_id: int, options: Options, kuma: Kuma) -> dict[str, Any]:
    return kuma.edit_notification(notification_id, **options)


@router.delete("/{notification_id}", summary="Delete a notification")
def delete_notification(notification_id: int, kuma: Kuma) -> dict[str, Any]:
    return kuma.delete_notification(notification_id)
