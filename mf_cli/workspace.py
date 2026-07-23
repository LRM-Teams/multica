"""Modelfactory workspace operations — create, list, restart, stop, save, delete."""

import json
import urllib.request
import urllib.error
from dataclasses import dataclass, field
from typing import Any

from .auth import get_token
from .config import (
    BASE_URL, API_WORKSPACE, API_BILLING_PRICE,
    DEFAULT_WORKSPACE_IMAGE, DEFAULT_RESOURCES,
)


@dataclass
class WorkspaceInfo:
    id: str = ""
    name: str = ""
    image: str = ""
    gpu_label: str = ""
    gpu_count: int = 0
    cpu: int = 0
    memory_mb: int = 0
    status: str = ""
    phases: set = field(default_factory=set)
    raw: dict = field(default_factory=dict)


def _api_request(token: str, method: str, path: str, body: dict | None = None) -> tuple[int, Any]:
    url = f"{BASE_URL}{path}"
    data = json.dumps(body).encode("utf-8") if body is not None else None
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("aimaster-token-header", token)
    req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            raw = resp.read().decode("utf-8")
            try:              return resp.status, json.loads(raw)
            except json.JSONDecodeError: return resp.status, raw
    except urllib.error.HTTPError as e:
        raw = e.read().decode("utf-8")
        try:              return e.code, json.loads(raw)
        except json.JSONDecodeError: return e.code, raw


def _get_price_token(token: str, gpu_label: str, cpu: int, memory_gb: int, gpu_count: int) -> str:
    status, resp = _api_request(token, "POST", API_BILLING_PRICE, {
        "items": [{"label": "CPU", "num": cpu}, {"label": "MEMORY", "num": memory_gb},
                  {"label": gpu_label, "num": gpu_count}], "isPt": False})
    if status != 200:
        raise RuntimeError(f"Failed to get price token: {resp}")
    return resp.get("price_token", "")


def _phase_status(phases: set) -> str:
    if not phases: return ""
    if 2 in phases and 4 not in phases and 5 not in phases: return "Running"
    if 1 in phases and 2 not in phases: return "Deploying"
    if 5 in phases: return "Saved"
    if 4 in phases: return "Stopped"
    return f"phase:{max(phases)}"


# --- CRUD ---

def create_workspace(
    username: str | None = None, password: str | None = None,
    name: str = "newWorkspace", image: str | None = None,
    gpu_label: str = "A800-8", gpu_count: int = 1,
    cpu: int = 8, memory_gb: int = 80, disk_gb: int = 2,
    ssh_enabled: bool = False, best_effort: bool = False,
    headless: bool = True,
) -> dict:
    token = get_token(username, password, headless=headless)
    if image is None: image = DEFAULT_WORKSPACE_IMAGE
    price_token = _get_price_token(token, gpu_label, cpu, memory_gb, gpu_count)
    memory_mib = memory_gb * 1024
    body = {
        "aliases": name, "image": image, "labels": gpu_label,
        "allowed_labels": [gpu_label], "ssh_enabled": ssh_enabled,
        "best_effort": best_effort,
        "resources": {"cpu": cpu, "nvidiaGpu": gpu_count, "gpu": 0,
                      "memoryInMi": memory_mib, "diskInGi": disk_gb, "priceToken": price_token},
    }
    status, resp = _api_request(token, "PUT", API_WORKSPACE, body)
    if status in (200, 201):
        return {"workspace_id": resp.get("message", ""), "name": name, "image": image,
                "gpu_label": gpu_label, "gpu_count": gpu_count, "status": "created"}
    raise RuntimeError(f"Create failed: HTTP {status}, {resp}")


def list_workspaces(
    username: str | None = None, password: str | None = None, headless: bool = True,
) -> list[WorkspaceInfo]:
    token = get_token(username, password, headless=headless)
    status, resp = _api_request(token, "GET", API_WORKSPACE)
    items = resp if isinstance(resp, list) else resp.get("items", resp.get("data", []))
    result = []
    for item in items:
        ent = item.get("entity", item)
        req = ent.get("request", {})
        res = req.get("resources", {})
        stats = item.get("status", [])
        phases = {s.get("phase") for s in stats if isinstance(s, dict)}
        result.append(WorkspaceInfo(
            id=ent.get("id", ""), name=req.get("aliases", ""),
            image=req.get("image", ent.get("image", ""))[:100],
            gpu_label=req.get("labels", ""), gpu_count=res.get("nvidiaGpu", 0),
            cpu=res.get("cpu", 0), memory_mb=res.get("memoryInMi", 0),
            status=_phase_status(phases), phases=phases, raw=item))
    return result


def get_workspace(job_id: str, **kwargs) -> WorkspaceInfo | None:
    for ws in list_workspaces(**kwargs):
        if ws.id == job_id or job_id in ws.id: return ws
    return None


# --- Actions ---

def _workspace_action(token: str, ws_id: str, action: str) -> dict:
    status, resp = _api_request(token, "POST", f"{API_WORKSPACE}/{ws_id}", {"action": action})
    if status == 200 and resp.get("code") == "S0000":
        return {"workspace_id": ws_id, "action": action, "result": "ok"}
    raise RuntimeError(f"{action} failed: {resp}")


def restart_workspace(ws_id: str, **kwargs) -> dict:
    token = get_token(kwargs.get("username"), kwargs.get("password"), headless=kwargs.get("headless", True))
    return _workspace_action(token, ws_id, "restart")


def stop_workspace(ws_id: str, **kwargs) -> dict:
    token = get_token(kwargs.get("username"), kwargs.get("password"), headless=kwargs.get("headless", True))
    return _workspace_action(token, ws_id, "stop")


def save_workspace(ws_id: str, **kwargs) -> dict:
    token = get_token(kwargs.get("username"), kwargs.get("password"), headless=kwargs.get("headless", True))
    return _workspace_action(token, ws_id, "save")


def delete_workspace(ws_id: str, **kwargs) -> dict:
    token = get_token(kwargs.get("username"), kwargs.get("password"), headless=kwargs.get("headless", True))
    status, resp = _api_request(token, "DELETE", f"{API_WORKSPACE}/{ws_id}")
    if status == 200:
        return {"workspace_id": ws_id, "result": "deleted"}
    raise RuntimeError(f"Delete failed: {resp}")
