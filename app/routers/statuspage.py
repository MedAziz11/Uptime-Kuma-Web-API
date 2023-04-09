from fastapi import APIRouter, Depends, HTTPException, Path, Body
from uptime_kuma_api import UptimeKumaApi, UptimeKumaException
from config import logger as logging
from schemas.api import API
from utils.deps import get_current_user
from schemas.statuspage import StatusPageList, StatusPage, AddStatusPageResponse, AddStatusPageRequest,SaveStatusPageRequest,SaveStatusPageResponse, DeleteStatusPageResponse

router = APIRouter(redirect_slashes=True)


@router.get("", response_model=StatusPageList, description="Get all status pages")
async def get_all_status_pages(cur_user: API = Depends(get_current_user)):
    api: UptimeKumaApi = cur_user['api']
    try:
        return {"statuspages": api.get_status_pages()}
    except Exception as e:
        logging.fatal(e)
        raise HTTPException(500, str(e))


@router.get("/{slug}", response_model=StatusPage, description="Get a status page")
async def get_status_page(slug: str, cur_user: API = Depends(get_current_user)):
    api: UptimeKumaApi = cur_user['api']
    try:
        return api.get_status_page(slug)
    except UptimeKumaException as e:
        logging.error(e)
        raise HTTPException(404, str(e))
    except Exception as e:
        logging.fatal(e)
        raise HTTPException(500, str(e))


@router.post("", response_model=AddStatusPageResponse, description="Add a status page")
async def add_status_page(status_page_data: AddStatusPageRequest, cur_user: API = Depends(get_current_user)):
    api: UptimeKumaApi = cur_user['api']
    try:
        return api.add_status_page(status_page_data.slug, status_page_data.title)
    except UptimeKumaException as e:
        logging.error(e)
        raise HTTPException(400, str(e))
    except Exception as e:
        logging.fatal(e)
        raise HTTPException(500, str(e))


@router.post("/{slug}",response_model=SaveStatusPageResponse, description="Save a status page")
async def save_status_page(
    slug: str = Path(...),
    status_page_data: SaveStatusPageRequest = Body(...),
    cur_user: API = Depends(get_current_user),
):  
    api: UptimeKumaApi = cur_user['api']
    try:
        print (status_page_data.id)
        return api.save_status_page(
            slug,
            id=status_page_data.id,
            title=status_page_data.title,
            description=status_page_data.description,
            theme=status_page_data.theme,
            published=status_page_data.published,
            showTags=status_page_data.showTags,
            domainNameList=status_page_data.domainNameList,
            googleAnalyticsId=status_page_data.googleAnalyticsId,
            customCSS=status_page_data.customCSS,
            footerText=status_page_data.footerText,
            showPoweredBy=status_page_data.showPoweredBy,
            icon=status_page_data.icon,
            publicGroupList=status_page_data.publicGroupList,
        )
    except UptimeKumaException as e:
        logging.error(e)
        raise HTTPException(400, str(e))
    except Exception as e:
        logging.fatal(e)
        raise HTTPException(500, str(e))

@router.delete("/{slug}", response_model=DeleteStatusPageResponse, description="Delete a status page")
async def delete_status_page(slug: str = Path(...), cur_user: API = Depends(get_current_user)):
    api: UptimeKumaApi = cur_user['api']
    try:
        return api.delete_status_page(slug)
    #Catch type error...which is actually a success, go figgure. {"detail":"'NoneType' object has no attribute 'values'"}
    except TypeError as e:
        if "NoneType" in str(e):
            logging.info("Status page deleted successfully")
            return {"status": "success"}
    except UptimeKumaException as e:
        logging.error(e)
        raise HTTPException(404, str(e))
    except Exception as e:
        logging.fatal(e)
        raise HTTPException(500, str(e))