from typing import Any

from fastapi import APIRouter

from app.deps import Kuma

router = APIRouter()


@router.get("", summary="Get server info")
def get_info(kuma: Kuma) -> dict[Any, Any]:
    return kuma.info()
