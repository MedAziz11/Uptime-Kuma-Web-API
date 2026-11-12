from typing import Annotated, Any

from fastapi import APIRouter, Body

from app.deps import Kuma

router = APIRouter()

# Options depend on the docker host type: the body is forwarded as-is and validated
# by uptime-kuma-api (invalid options return 422).
Options = Annotated[
    dict[str, Any],
    Body(
        examples=[{"name": "local", "dockerType": "socket", "dockerDaemon": "/var/run/docker.sock"}]
    ),
]


@router.get("", summary="Get all docker hosts")
def get_docker_hosts(kuma: Kuma) -> dict[str, Any]:
    return {"docker_hosts": kuma.get_docker_hosts()}


@router.post("", summary="Add a docker host")
def add_docker_host(options: Options, kuma: Kuma) -> dict[str, Any]:
    return kuma.add_docker_host(**options)


@router.post("/test", summary="Test a docker host")
def test_docker_host(options: Options, kuma: Kuma) -> dict[str, Any]:
    return kuma.test_docker_host(**options)


@router.get("/{docker_host_id}", summary="Get a docker host")
def get_docker_host(docker_host_id: int, kuma: Kuma) -> dict[str, Any]:
    return {"docker_host": kuma.get_docker_host(docker_host_id)}


@router.patch("/{docker_host_id}", summary="Update a docker host")
def update_docker_host(docker_host_id: int, options: Options, kuma: Kuma) -> dict[str, Any]:
    return kuma.edit_docker_host(docker_host_id, **options)


@router.delete("/{docker_host_id}", summary="Delete a docker host")
def delete_docker_host(docker_host_id: int, kuma: Kuma) -> dict[str, Any]:
    return kuma.delete_docker_host(docker_host_id)
