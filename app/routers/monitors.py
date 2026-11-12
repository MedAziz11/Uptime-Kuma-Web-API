from typing import Annotated, Any

from fastapi import APIRouter, Query

from app.deps import Kuma
from app.schemas.monitor import Monitor, MonitorTag, MonitorUpdate

router = APIRouter()


@router.get("", summary="Get all monitors")
def get_monitors(kuma: Kuma) -> dict[str, Any]:
    return {"monitors": kuma.get_monitors()}


@router.post("", summary="Create a monitor")
def create_monitor(monitor: Monitor, kuma: Kuma) -> dict[str, Any]:
    return kuma.add_monitor(**monitor.model_dump(exclude_unset=True))


@router.get("/{monitor_id}", summary="Get a monitor")
def get_monitor(monitor_id: int, kuma: Kuma) -> dict[str, Any]:
    return {"monitor": kuma.get_monitor(monitor_id)}


@router.patch("/{monitor_id}", summary="Update a monitor")
def update_monitor(monitor_id: int, monitor: MonitorUpdate, kuma: Kuma) -> dict[str, Any]:
    changes = monitor.model_dump(exclude_unset=True)
    resp = kuma.edit_monitor(monitor_id, **changes)
    return {**resp, "monitor_data": changes}


@router.delete("/{monitor_id}", summary="Delete a monitor")
def delete_monitor(monitor_id: int, kuma: Kuma) -> dict[str, Any]:
    return kuma.delete_monitor(monitor_id)


@router.post("/{monitor_id}/pause", summary="Pause a monitor")
def pause_monitor(monitor_id: int, kuma: Kuma) -> dict[str, Any]:
    return kuma.pause_monitor(monitor_id)


@router.post("/{monitor_id}/resume", summary="Resume a monitor")
def resume_monitor(monitor_id: int, kuma: Kuma) -> dict[str, Any]:
    return kuma.resume_monitor(monitor_id)


@router.get("/{monitor_id}/beats", summary="Get the heartbeats of a monitor for the last N hours")
def monitor_beats(
    monitor_id: int, kuma: Kuma, hours: Annotated[float, Query(gt=0)] = 1
) -> dict[str, Any]:
    return {"monitor_beats": kuma.get_monitor_beats(monitor_id, hours)}


@router.post("/{monitor_id}/tag", summary="Add an existing tag to a monitor")
def add_monitor_tag(monitor_id: int, tag: MonitorTag, kuma: Kuma) -> dict[str, Any]:
    return kuma.add_monitor_tag(monitor_id=monitor_id, **tag.model_dump())


@router.delete("/{monitor_id}/tag", summary="Remove a tag from a monitor")
def delete_monitor_tag(monitor_id: int, tag: MonitorTag, kuma: Kuma) -> dict[str, Any]:
    return kuma.delete_monitor_tag(monitor_id=monitor_id, **tag.model_dump())
