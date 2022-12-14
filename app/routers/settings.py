from fastapi import APIRouter, Depends, HTTPException
from fastapi.encoders import jsonable_encoder
from uptime_kuma_api import UptimeKumaApi, UptimeKumaException

from config import logger as logging
from schemas.api import API
from schemas.settings import Backup,  ImportHandleType
from utils.deps import get_current_user
 
router = APIRouter()


@router.post("/upload_backup", description="Upload a Backup")
async def upload_backup(backup: Backup, import_handle: ImportHandleType, cur_user: API = Depends(get_current_user)):
    api : UptimeKumaApi = cur_user['api']
    try :
        return api.upload_backup(backup.json(), import_handle)
    except Exception as e:
        logging.fatal(e)
        raise HTTPException(500, str(e))
