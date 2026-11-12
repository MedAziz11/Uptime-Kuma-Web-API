from typing import Any

from fastapi import APIRouter

from app.deps import Kuma
from app.schemas.maintenance import Maintenance, MaintenanceUpdate, MonitorMaintenance

router = APIRouter()


@router.get("", summary="Get all maintenances")
def get_maintenances(kuma: Kuma) -> dict[str, Any]:
    return {"maintenances": kuma.get_maintenances()}


@router.post("", summary="Create a maintenance")
def create_maintenance(maintenance: Maintenance, kuma: Kuma) -> dict[str, Any]:
    return kuma.add_maintenance(**maintenance.model_dump(exclude_unset=True))


@router.get("/{maintenance_id}", summary="Get a maintenance")
def get_maintenance(maintenance_id: int, kuma: Kuma) -> dict[str, Any]:
    return {"maintenance": kuma.get_maintenance(maintenance_id)}


@router.patch("/{maintenance_id}", summary="Update a maintenance")
def update_maintenance(
    maintenance_id: int, maintenance: MaintenanceUpdate, kuma: Kuma
) -> dict[str, Any]:
    changes = maintenance.model_dump(exclude_unset=True)
    resp = kuma.edit_maintenance(maintenance_id, **changes)
    return {**resp, "maintenance_data": changes}


@router.delete("/{maintenance_id}", summary="Delete a maintenance")
def delete_maintenance(maintenance_id: int, kuma: Kuma) -> dict[str, Any]:
    return kuma.delete_maintenance(maintenance_id)


@router.post("/{maintenance_id}/pause", summary="Pause a maintenance")
def pause_maintenance(maintenance_id: int, kuma: Kuma) -> dict[str, Any]:
    return kuma.pause_maintenance(maintenance_id)


@router.post("/{maintenance_id}/resume", summary="Resume a maintenance")
def resume_maintenance(maintenance_id: int, kuma: Kuma) -> dict[str, Any]:
    return kuma.resume_maintenance(maintenance_id)


@router.get("/{maintenance_id}/monitors", summary="Get the monitors of a maintenance")
def get_monitor_maintenance(maintenance_id: int, kuma: Kuma) -> list[dict[str, Any]]:
    return kuma.get_monitor_maintenance(maintenance_id)


@router.post("/{maintenance_id}/monitors", summary="Set the monitors of a maintenance")
def add_monitor_maintenance(
    maintenance_id: int, monitors: list[MonitorMaintenance], kuma: Kuma
) -> dict[str, Any]:
    return kuma.add_monitor_maintenance(maintenance_id, [{"id": m.id} for m in monitors])
