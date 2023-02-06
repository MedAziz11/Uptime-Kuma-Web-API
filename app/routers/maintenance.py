from fastapi import APIRouter, Depends, HTTPException, Path
from uptime_kuma_api import UptimeKumaApi, UptimeKumaException
from typing import List

from schemas.maintenance import Maintenance, MaintenanceUpdate, MonitorMaintenance
from schemas.api import API
from utils.deps import get_current_user
from config import logger as logging



router = APIRouter(redirect_slashes=True)

@router.get("", description="Get all Maintenances")
async def get_maintenances(cur_user: API = Depends(get_current_user)):
    api: UptimeKumaApi = cur_user['api']
    try :
        return {"maintenances":  api.get_maintenances()}
    except Exception as e :
        logging.fatal(e)
        raise HTTPException(500, str(e))


@router.get("/{maintenance_id}", description="Get Maintenance By ID")
async def get_maintenance(maintenance_id:int=Path(...) , cur_user: API = Depends(get_current_user)):
    api: UptimeKumaApi = cur_user['api']
 
    if maintenance_id :
        try:
            maintenance = api.get_maintenance(maintenance_id)
        except UptimeKumaException as e:
            logging.info(e)
            raise HTTPException(404, {"message": "Maintenance not found !"})
        except Exception as e :
            logging.fatal(e)
            raise HTTPException(500, str(e))

        return {"maintenance": maintenance }

    raise HTTPException(404, {"message": "Maintenance not found !"})



@router.post("", description="Create a Maintenance")
async def create_maintenance(maintenance: Maintenance,cur_user: API = Depends(get_current_user)):
    api: UptimeKumaApi = cur_user['api']
    try:
        resp = api.add_maintenance(**maintenance.dict())
    except TypeError as e:
        logging.error(e)
        raise HTTPException(422, str(e))
    except Exception as e :
        logging.fatal(e)
        raise HTTPException(500, str(e))

    return resp



@router.patch("/{maintenance_id}", description="Update a specific Maintenance")
async def update_maintenance(maintenance: MaintenanceUpdate,maintenance_id:int=Path(...) ,cur_user: API = Depends(get_current_user)):
    api: UptimeKumaApi = cur_user['api']
    try:
        resp = api.edit_maintenance(id_=maintenance_id, **maintenance.dict(exclude_unset=True))
    except UptimeKumaException as e:
        logging.info(e)
        raise HTTPException(404, {"message": "Maintenance not found !"})
    except TypeError as e:
        logging.error(e)
        raise HTTPException(422, str(e) )
    except Exception as e :
        logging.fatal(e)
        raise HTTPException(500, str(e))

    return {**resp, "maintenance_data":maintenance.dict(exclude_unset=True)}



@router.delete("/{maintenance_id}", description="Delete a specific Maintenance")
async def delete_maintenance(maintenance_id:int=Path(...) ,cur_user: API = Depends(get_current_user)):
    api: UptimeKumaApi = cur_user['api']
    try:
        # kinda dumb the api doesnt check if th id exists he just sends an event
        resp = api.delete_maintenance(maintenance_id)
    except UptimeKumaException as e:
        logging.info(e)
        raise HTTPException(404, {"message": "Maintenance not found !"})
    except Exception as e :
        logging.fatal(e)
        raise HTTPException(500, str(e))

    return resp



@router.post("/{maintenance_id}/pause", description="Pause a specific maintenance")
async def pause_maintenance(maintenance_id:int=Path(...) ,cur_user: API = Depends(get_current_user)):
    api: UptimeKumaApi = cur_user['api']
    try:
        resp = api.pause_maintenance(maintenance_id)
    except UptimeKumaException as e:
        logging.info(e)
        raise HTTPException(404, {"message": "maintenance not found !"})
    except Exception as e :
        logging.fatal(e)
        raise HTTPException(500, str(e))

    return resp



@router.post("/{maintenance_id}/resume", description="Resume a specific maintenance")
async def resume_maintenance(maintenance_id:int=Path(...) ,cur_user: API = Depends(get_current_user)):
    api: UptimeKumaApi = cur_user['api']
    try:
        resp = api.resume_maintenance(maintenance_id)
    except UptimeKumaException as e:
        logging.info(e)
        raise HTTPException(404, {"message": "maintenance not found !"})
    except Exception as e :
        logging.fatal(e)
        raise HTTPException(500, str(e))

    return resp

@router.get("/{maintenance_id}/monitors", description="Get monitors to a maintenance.")
async def add_monitor_maintenance(maintenance_id:int=Path(...),cur_user: API = Depends(get_current_user))-> List[MonitorMaintenance]:
    api: UptimeKumaApi = cur_user['api']
    try:
        monitors = api.get_monitor_maintenance(maintenance_id)
    except UptimeKumaException as e:
        logging.info(e)
        raise HTTPException(404, {"message": f"maintenance not found ! "})
    except Exception as e :
        logging.fatal(e)
        raise HTTPException(500, str(e))

    return monitors

@router.post("/{maintenance_id}/monitors", description="Adds monitors to a maintenance.")
async def add_monitor_maintenance(monitors: List[MonitorMaintenance], maintenance_id:int=Path(...),cur_user: API = Depends(get_current_user)):
    api: UptimeKumaApi = cur_user['api']
    try:
        mns = [m.dict() for m in monitors]
        resp = api.add_monitor_maintenance(maintenance_id, mns)
    except UptimeKumaException as e:
        logging.info(e)
        raise HTTPException(404, {"message": f"maintenance or monitor not found !"})
    except Exception as e :
        logging.fatal(e)
        raise HTTPException(500, str(e))

    return resp