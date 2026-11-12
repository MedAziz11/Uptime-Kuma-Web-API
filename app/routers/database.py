from typing import Any

from fastapi import APIRouter

from app.deps import Kuma

router = APIRouter()


@router.get("/size", summary="Get the Uptime Kuma database size")
def get_db_size(kuma: Kuma) -> dict[str, Any]:
    return {**kuma.get_database_size(), "unit": "octet"}


@router.post("/shrink", summary="Shrink the Uptime Kuma database")
def shrink_db(kuma: Kuma) -> dict[str, Any]:
    return {"message": "DB Shrinked", "details": kuma.shrink_database()}
