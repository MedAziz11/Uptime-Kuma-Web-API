from typing import Any

from fastapi import APIRouter

from app.deps import Kuma

router = APIRouter()


@router.get("", summary="Get certificate info")
def get_cert_info(kuma: Kuma) -> dict[Any, Any]:
    return kuma.cert_info()
