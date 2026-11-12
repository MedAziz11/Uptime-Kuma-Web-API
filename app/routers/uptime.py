from typing import Any

from fastapi import APIRouter

from app.deps import Kuma

router = APIRouter()


@router.get("", summary="Get the uptime of each monitor")
def get_uptime(kuma: Kuma) -> dict[Any, Any]:
    return kuma.uptime()
