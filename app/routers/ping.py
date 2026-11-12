from typing import Any

from fastapi import APIRouter

from app.deps import Kuma

router = APIRouter()


@router.get("", summary="Get the average ping of each monitor")
def get_avg_ping(kuma: Kuma) -> dict[Any, Any]:
    return kuma.monitor_stats("avg_ping")
