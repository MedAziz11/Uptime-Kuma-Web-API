from fastapi import APIRouter, Depends, HTTPException
from uptime_kuma_api import UptimeKumaApi, UptimeKumaException

from config import logger as logging
from schemas.api import API
from utils.deps import get_current_user
 
router = APIRouter(redirect_slashes=True)


@router.get("/size", description="Get Database Size")
async def get_db_size(cur_user: API = Depends(get_current_user)):
    api : UptimeKumaApi = cur_user['api']
    try:
        resp = api.get_database_size()
        return {**resp, "unit": "octet"}
    except Exception as e:
        logging.fatal(e)
        raise HTTPException(500, str(e))


@router.post("/shrink", description="Shrink Database")
async def shrink_db(cur_user: API = Depends(get_current_user)):
    api : UptimeKumaApi = cur_user['api']
    try:
        resp = api.shrink_database()
        return {"message": "Database shrinked" , "details": resp}
    except Exception as e:
        logging.fatal(e)
        raise HTTPException(500, str(e))
