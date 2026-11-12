from typing import Any

from fastapi import APIRouter

from app.deps import Kuma
from app.schemas.tag import Tag, TagUpdate

router = APIRouter()


@router.get("", summary="Get all tags")
def get_tags(kuma: Kuma) -> dict[str, Any]:
    return {"tags": kuma.get_tags()}


@router.post("", summary="Add a tag")
def add_tag(tag: Tag, kuma: Kuma) -> dict[str, Any]:
    return kuma.add_tag(**tag.model_dump())


@router.get("/{tag_id}", summary="Get a tag")
def get_tag(tag_id: int, kuma: Kuma) -> dict[str, Any]:
    return {"tag": kuma.get_tag(tag_id)}


@router.patch("/{tag_id}", summary="Update a tag")
def update_tag(tag_id: int, tag: TagUpdate, kuma: Kuma) -> dict[str, Any]:
    return kuma.edit_tag(tag_id, **tag.model_dump(exclude_unset=True))


@router.delete("/{tag_id}", summary="Delete a tag")
def delete_tag(tag_id: int, kuma: Kuma) -> dict[str, Any]:
    return kuma.delete_tag(tag_id)
