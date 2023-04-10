from fastapi import APIRouter, Depends, Path, Body
from uptime_kuma_api import UptimeKumaApi, UptimeKumaException
from config import logger as logging
from schemas.api import API
from utils.deps import get_current_user
from utils.exceptions import handle_api_exceptions
from schemas.statuspage import (
    StatusPageList,
    StatusPage,
    AddStatusPageResponse,
    AddStatusPageRequest,
    SaveStatusPageRequest,
    SaveStatusPageResponse,
    DeleteStatusPageResponse,
)
import json

router = APIRouter(redirect_slashes=True)


@router.get("", response_model=StatusPageList, description="Get all status pages")
async def get_all_status_pages(cur_user: API = Depends(get_current_user)):
    api: UptimeKumaApi = cur_user["api"]
    return {"statuspages": await handle_api_exceptions(api.get_status_pages)}


@router.get("/{slug}", response_model=StatusPage, description="Get a status page")
async def get_status_page(slug: str, cur_user: API = Depends(get_current_user)):
    api: UptimeKumaApi = cur_user["api"]
    return await handle_api_exceptions(api.get_status_page, slug)


@router.post("", response_model=AddStatusPageResponse, description="Add a status page")
async def add_status_page(
    status_page_data: AddStatusPageRequest, cur_user: API = Depends(get_current_user)
):
    api: UptimeKumaApi = cur_user["api"]
    return await handle_api_exceptions(
        api.add_status_page, status_page_data.slug, status_page_data.title
    )


@router.post(
    "/{slug}", response_model=SaveStatusPageResponse, description="Save a status page"
)
async def save_status_page(
    slug: str = Path(...),
    status_page_data: SaveStatusPageRequest = Body(...),
    cur_user: API = Depends(get_current_user),
):
    api: UptimeKumaApi = cur_user["api"]

    async def save_status_page_wrapper(
        slug: str, status_page_data: SaveStatusPageRequest
    ):
        logging.info(
            f"Saving status page with slug={slug}, status_page_data={json.dumps(status_page_data.dict())}"
        )
        return await api.save_status_page(
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

    return await handle_api_exceptions(save_status_page_wrapper, slug, status_page_data)


@router.delete(
    "/{slug}",
    response_model=DeleteStatusPageResponse,
    description="Delete a status page",
)
async def delete_status_page(
    slug: str = Path(...), cur_user: API = Depends(get_current_user)
):
    api: UptimeKumaApi = cur_user["api"]

    def delete_status_page_api(slug):
        logging.info(f"Deleting status page with slug={slug}")
        try:
            result = api.delete_status_page(slug)
            return {"detail": "success"}
        except UptimeKumaException as e:
            raise e
        # catch all other exceptions
        except Exception as e:
            # Exception: 'NoneType' object has no attribute 'values'
            if "NoneType" in str(e):
                logging.info(f"Exception: {e}")
                return {"detail": "success"}

    return await handle_api_exceptions(delete_status_page_api, slug)
