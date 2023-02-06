from fastapi import APIRouter, Depends, HTTPException, Path
from uptime_kuma_api import UptimeKumaApi, UptimeKumaException

from schemas.monitor import Monitor, MonitorUpdate, MonitorTag
from schemas.api import API
from utils.deps import get_current_user
from config import logger as logging


router = APIRouter(redirect_slashes=True)

@router.get("", description="Get all Monitors")
async def get_monitors(cur_user: API = Depends(get_current_user)):
    api: UptimeKumaApi = cur_user['api']
    try :
        return {"monitors":  api.get_monitors()}
    except Exception as e :
        logging.fatal(e)
        raise HTTPException(500, str(e))

@router.get("/{monitor_id}", description="Get Monitor By ID")
async def get_monitor(monitor_id:int=Path(...) , cur_user: API = Depends(get_current_user)):
    api: UptimeKumaApi = cur_user['api']
 
    if monitor_id :
        try:
            monitor = api.get_monitor(monitor_id)
        except UptimeKumaException as e:
            logging.info(e)
            raise HTTPException(404, {"message": "Monitor not found !"})
        except Exception as e :
            logging.fatal(e)
            raise HTTPException(500, str(e))

        return {"monitor": monitor }

    raise HTTPException(404, {"message": "Monitor not found !"})


@router.post("", description="Create a Monitor")
async def create_monitor(monitor: Monitor,cur_user: API = Depends(get_current_user)):
    api: UptimeKumaApi = cur_user['api']
    try:
        resp = api.add_monitor(**monitor.dict())
    except TypeError as e:
        logging.error(e)
        raise HTTPException(422, str(e) )
    except Exception as e :
        logging.fatal(e)
        raise HTTPException(500, str(e))

    return resp

@router.patch("/{monitor_id}", description="Update a specific Monitor")
async def update_monitor(monitor: MonitorUpdate,monitor_id:int=Path(...) ,cur_user: API = Depends(get_current_user)):
    api: UptimeKumaApi = cur_user['api']
    try:
        resp = api.edit_monitor(id_=monitor_id, **monitor.dict(exclude_unset=True))
    except UptimeKumaException as e:
        logging.info(e)
        raise HTTPException(404, {"message": "Monitor not found !"})
    except TypeError as e:
        logging.error(e)
        raise HTTPException(422, str(e) )
    except Exception as e :
        logging.fatal(e)
        raise HTTPException(500, str(e))

    return {**resp, "monitor_data":monitor.dict(exclude_unset=True)}

@router.delete("/{monitor_id}", description="Delete a specific Monitor")
async def delete_monitor(monitor_id:int=Path(...) ,cur_user: API = Depends(get_current_user)):
    api: UptimeKumaApi = cur_user['api']
    try:
        # kinda dumb the api doesnt check if th id exists he just sends an event
        resp = api.delete_monitor(monitor_id)
    except UptimeKumaException as e:
        logging.info(e)
        raise HTTPException(404, {"message": "Monitor not found !"})
    except Exception as e :
        logging.fatal(e)
        raise HTTPException(500, str(e))

    return resp

@router.post("/{monitor_id}/pause", description="Pause a specific Monitor")
async def pause_monitor(monitor_id:int=Path(...) ,cur_user: API = Depends(get_current_user)):
    api: UptimeKumaApi = cur_user['api']
    try:
        resp = api.pause_monitor(monitor_id)
    except UptimeKumaException as e:
        logging.info(e)
        raise HTTPException(404, {"message": "Monitor not found !"})
    except Exception as e :
        logging.fatal(e)
        raise HTTPException(500, str(e))

    return resp



@router.post("/{monitor_id}/resume", description="Resume a specific Monitor")
async def resume_monitor(monitor_id:int=Path(...) ,cur_user: API = Depends(get_current_user)):
    api: UptimeKumaApi = cur_user['api']
    try:
        resp = api.resume_monitor(monitor_id)
    except UptimeKumaException as e:
        logging.info(e)
        raise HTTPException(404, {"message": "Monitor not found !"})
    except Exception as e :
        logging.fatal(e)
        raise HTTPException(500, str(e))

    return resp


@router.get("/{monitor_id}/beats", description="Get Monitor Beats in the last N hours ( by default its 1 hour) ")
async def monitor_beats(monitor_id:int=Path(...), hours:float=1 , cur_user: API = Depends(get_current_user)):
    api: UptimeKumaApi = cur_user['api']
    if monitor_id :
        try:
            monitor = api.get_monitor_beats(monitor_id, hours)
        except UptimeKumaException as e:
            logging.info(e)
            raise HTTPException(404, {"message": "Monitor not found !"})
        except Exception as e :
            logging.fatal(e)
            raise HTTPException(500, str(e))

        if monitor :
            return {"monitor_beats": monitor }
        
        raise HTTPException(404, {"message": "Monitor not found !"})

    raise HTTPException(404, {"message": "Monitor not found !"})


# to be implemented 
# api.get_monitor_tags   api.edit_monitor_tags api.delete_monitor_tags


@router.post("/{monitor_id}/tag", description="Add an already created tag to a specific monitor")
async def add_monitor_tag(tag: MonitorTag ,monitor_id:int=Path(...),  cur_user: API = Depends(get_current_user)):
    api: UptimeKumaApi = cur_user['api']
    if monitor_id :
        try:
            msg = api.add_monitor_tag(monitor_id=monitor_id, **tag.dict())
        except UptimeKumaException as e:
            logging.info(e)
            raise HTTPException(404, {"message": "Monitor or Tag not found !"})
        except Exception as e :
            logging.fatal(e)
            raise HTTPException(500, str(e))


        return msg
    
    raise HTTPException(404, {"message": "Monitor not found !"})

@router.delete("/{monitor_id}/tag", description="Delete a tag from a specific monitor")
async def delete_monitor_tag(tag: MonitorTag ,monitor_id:int=Path(...),  cur_user: API = Depends(get_current_user)):
    api: UptimeKumaApi = cur_user['api']
    if monitor_id :
        try:
            msg = api.delete_monitor_tag(monitor_id=monitor_id, **tag.dict())
        except UptimeKumaException as e:
            logging.info(e)
            raise HTTPException(404, {"message": "Monitor or Tag not found !"})
        except Exception as e :
            logging.fatal(e)
            raise HTTPException(500, str(e))


        return msg
    
    raise HTTPException(404, {"message": "Monitor not found !"})