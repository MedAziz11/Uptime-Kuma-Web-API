from typing import Annotated, Any

from fastapi import APIRouter, Body

from app.deps import Kuma

router = APIRouter()

# Options depend on the proxy type: the body is forwarded as-is and validated
# by uptime-kuma-api (invalid options return 422).
Options = Annotated[
    dict[str, Any], Body(examples=[{"protocol": "http", "host": "127.0.0.1", "port": 8080}])
]


@router.get("", summary="Get all proxies")
def get_proxies(kuma: Kuma) -> dict[str, Any]:
    return {"proxies": kuma.get_proxies()}


@router.post("", summary="Add a proxy")
def add_proxy(options: Options, kuma: Kuma) -> dict[str, Any]:
    return kuma.add_proxy(**options)


@router.get("/{proxy_id}", summary="Get a proxy")
def get_proxy(proxy_id: int, kuma: Kuma) -> dict[str, Any]:
    return {"proxy": kuma.get_proxy(proxy_id)}


@router.patch("/{proxy_id}", summary="Update a proxy")
def update_proxy(proxy_id: int, options: Options, kuma: Kuma) -> dict[str, Any]:
    return kuma.edit_proxy(proxy_id, **options)


@router.delete("/{proxy_id}", summary="Delete a proxy")
def delete_proxy(proxy_id: int, kuma: Kuma) -> dict[str, Any]:
    return kuma.delete_proxy(proxy_id)
