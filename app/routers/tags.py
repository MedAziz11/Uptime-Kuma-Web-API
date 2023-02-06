from typing import Dict, List
from fastapi import APIRouter, Depends, HTTPException, Path
from uptime_kuma_api import UptimeKumaApi, UptimeKumaException


from utils.deps import get_current_user
from schemas.api import API
from schemas.tag import Tag, TagUpdate
from config import logger as logging




router = APIRouter(redirect_slashes=True)

@router.post("", description="Add a tag by name and color")
async def add_tags(tag:Tag, cur_user: API = Depends(get_current_user)):
    api: UptimeKumaApi = cur_user['api']
    try:
        resp = api.add_tag(**tag.dict())
    except TypeError as e:
        logging.error(e)
        raise HTTPException(422, str(e) )
    except Exception as e :
        logging.fatal(e)
        raise HTTPException(500, str(e))

    return resp

@router.get("", description="Get all tags")
async def get_tags(cur_user: API = Depends(get_current_user))-> Dict[str, List[Dict]]:
    api: UptimeKumaApi = cur_user['api']
    try :
        return {"tags": api.get_tags()}
    except Exception as e :
        logging.fatal(e)
        raise HTTPException(500, str(e))

@router.get("/{tag_id}", description="Get a Tag By ID")
async def get_tag(tag_id: int=Path(...), cur_user: API = Depends(get_current_user)):
    api: UptimeKumaApi = cur_user['api']
    try:
        tag = api.get_tag(tag_id)
    except UptimeKumaException as e:
        logging.info(e)
        raise HTTPException(404, {"message": "Tag not found !"})
    except Exception as e :
        logging.fatal(e)
        raise HTTPException(500, str(e))

    return { "tag" :  tag }


@router.delete("/{tag_id}", description="Delete a specific Tag By ID")
async def delete_tag(tag_id:int=Path(...) , cur_user: API = Depends(get_current_user)):
    api: UptimeKumaApi = cur_user['api']
    try:
        # kinda dumb the api doesnt check if th id exists he just sends an event
        resp = api.delete_tag(tag_id)
    except UptimeKumaException as e:
        logging.info(e)
        raise HTTPException(404, {"message": "Tag not found !"})
    except Exception as e :
        logging.fatal(e)
        raise HTTPException(500, str(e))

    return resp


# @router.patch("/{monitor_id}", description="Update a specific Tag By ID")
# async def update_tag(tag: TagUpdate, tag_id: int=Path(...), cur_user: API = Depends(get_current_user)):
#     api: UptimeKumaApi = cur_user['api']
#     
#     still not implemented in the library

