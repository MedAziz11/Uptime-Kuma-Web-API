from typing import Any

from fastapi import APIRouter

from app.deps import Kuma
from app.schemas.settings import Backup, ImportHandleType

router = APIRouter()


@router.post("/upload_backup", summary="Import a backup")
def upload_backup(
    backup: Backup, kuma: Kuma, import_handle: ImportHandleType = ImportHandleType.SKIP
) -> dict[str, Any]:
    return kuma.upload_backup(backup.model_dump_json(exclude_unset=True), import_handle.value)
