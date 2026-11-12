from typing import Any

from fastapi import APIRouter

from app.deps import Kuma
from app.schemas.statuspage import (
    AddStatusPageRequest,
    AddStatusPageResponse,
    Detail,
    Incident,
    PostIncidentRequest,
    SaveStatusPageRequest,
    StatusPage,
    StatusPageList,
)

router = APIRouter()


@router.get(
    "",
    response_model=StatusPageList,
    response_model_exclude_unset=True,
    summary="Get all status pages",
)
def get_all_status_pages(kuma: Kuma) -> dict[str, Any]:
    return {"statuspages": kuma.get_status_pages()}


@router.post("", response_model=AddStatusPageResponse, summary="Add a status page")
def add_status_page(status_page: AddStatusPageRequest, kuma: Kuma) -> dict[str, Any]:
    return kuma.add_status_page(status_page.slug, status_page.title)


@router.get(
    "/{slug}",
    response_model=StatusPage,
    response_model_exclude_unset=True,
    summary="Get a status page",
)
def get_status_page(slug: str, kuma: Kuma) -> dict[str, Any]:
    return kuma.get_status_page(slug)


@router.post("/{slug}", response_model=Detail, summary="Save a status page")
def save_status_page(slug: str, status_page: SaveStatusPageRequest, kuma: Kuma) -> dict[str, Any]:
    return {"detail": kuma.save_status_page(slug, **status_page.model_dump(exclude_unset=True))}


@router.delete("/{slug}", response_model=Detail, summary="Delete a status page")
def delete_status_page(slug: str, kuma: Kuma) -> dict[str, Any]:
    kuma.delete_status_page(slug)
    return {"detail": "success"}


@router.post(
    "/{slug}/incident",
    response_model=Incident,
    response_model_exclude_unset=True,
    summary="Post an incident",
)
def post_incident(slug: str, incident: PostIncidentRequest, kuma: Kuma) -> dict[str, Any]:
    return kuma.post_incident(slug, incident.title, incident.content, incident.style)


@router.delete("/{slug}/incident/unpin", response_model=Detail, summary="Unpin the incident")
def unpin_incident(slug: str, kuma: Kuma) -> dict[str, Any]:
    kuma.unpin_incident(slug)
    return {"detail": "success"}
