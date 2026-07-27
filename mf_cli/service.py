"""Modelfactory service operations — create, list, delete, status."""

import json
import urllib.error
import urllib.request
from dataclasses import dataclass, field
from typing import Any

from .auth import get_token
from .config import (
    API_BILLING_PRICE,
    API_INFERENCE_LARGE,
    BASE_URL,
    DEFAULT_RESOURCES,
)


@dataclass
class ServiceInfo:
    """Information about a Modelfactory service."""

    id: str = ""
    name: str = ""
    model_path: str = ""
    gpu_label: str = ""
    gpu_count: int = 1
    engine: str = ""
    status: str = ""
    raw: dict = field(default_factory=dict)


def _api_request(
    token: str,
    method: str,
    path: str,
    body: dict | None = None,
) -> tuple[int, Any]:
    """Make an authenticated API request. Returns (status_code, parsed_json)."""
    url = f"{BASE_URL}{path}"
    data = None
    if body is not None:
        data = json.dumps(body).encode("utf-8")

    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("aimaster-token-header", token)
    req.add_header("Content-Type", "application/json")

    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            raw = resp.read().decode("utf-8")
            try:
                return resp.status, json.loads(raw)
            except json.JSONDecodeError:
                return resp.status, raw
    except urllib.error.HTTPError as e:
        raw = e.read().decode("utf-8")
        try:
            return e.code, json.loads(raw)
        except json.JSONDecodeError:
            return e.code, raw


def _get_price_token(token: str, gpu_label: str, cpu: int, memory: int) -> str:
    """Get a billing price token for the requested resources."""
    status, resp = _api_request(
        token,
        "POST",
        API_BILLING_PRICE,
        {
            "items": [
                {"label": "CPU", "num": cpu},
                {"label": "MEMORY", "num": memory},
                {"label": gpu_label, "num": 1},
            ],
            "isPt": False,
        },
    )
    if status != 200:
        raise RuntimeError(f"Failed to get price token: {resp}")
    return resp.get("price_token", "")


def _gpu_label_to_spec_key(gpu_label: str) -> str:
    """Extract the spec key from a GPU label, e.g. 'A800-8' -> 'a800'."""
    return gpu_label.split("-")[0].lower()


def create_service(
    username: str | None = None,
    password: str | None = None,
    name: str = "newService",
    model_path: str = "",
    gpu_label: str = "A800-8",
    engine: str = "vllm-openai",
    engine_version: str = "v0.24.0",
    cpu: int | None = None,
    memory: int | None = None,
    replicas: int = 1,
    headless: bool = True,
    **kwargs,
) -> dict:
    """Create a large-model inference service on Modelfactory.

    Args:
        username: Modelfactory username (or use cached)
        password: Modelfactory password (or use cached)
        name: Service name / alias
        model_path: DFS path to the model checkpoint
        gpu_label: GPU spec label, e.g. 'A800-8', 'A100-8'
        engine: Inference engine, e.g. 'vllm-openai'
        engine_version: Engine version, e.g. 'v0.24.0'
        cpu: CPU cores (auto-detected from GPU spec if None)
        memory: Memory in MiB (auto-detected from GPU spec if None)
        replicas: Number of replicas
        headless: Run browser headless during login

    Returns:
        dict with 'service_id', 'name', 'status', etc.
    """
    token = get_token(username, password, headless=headless)

    # Auto-detect resources from GPU spec
    spec_key = _gpu_label_to_spec_key(gpu_label)
    defaults = DEFAULT_RESOURCES.get(
        spec_key, {"cpu": 8, "memoryInMi": 81920, "diskInGi": 8, "nvidiaGpu": 1}
    )

    if cpu is None:
        cpu = defaults["cpu"]
    if memory is None:
        memory = defaults["memoryInMi"]

    # Get billing price token
    price_token = _get_price_token(token, gpu_label, cpu, memory)

    # Build the create request body
    image_tag = f"{engine}:{engine_version}-vllm"
    body = {
        "aliases": name,
        "info": {
            "name": "",
            "service_type": engine,
            "model_name": "",
            "model_path": model_path,
            "model_type": "custom",
            "image": f"registry.docker.aimaster.lenovo.com:20443/library/public/inference/{image_tag}",
            "source": "custom",
            "deploy_type": "nlp",
            "letrain_id": "",
            "envs": "",
            "advance_params": "",
            "model": "qwen",
        },
        "resources": {
            "labels": gpu_label,
            "replicas": replicas,
            "resources_per_service": {
                "cpu": cpu,
                "nvidiaGpu": defaults["nvidiaGpu"],
                "gpu": 0,
                "memoryInMi": memory,
                "diskInGi": defaults["diskInGi"],
                "priceToken": "",
            },
            "priceToken": price_token,
            "instances": replicas,
        },
    }

    status, resp = _api_request(token, "POST", API_INFERENCE_LARGE, body)

    if status == 200 and resp.get("code") == "S0000":
        return {
            "service_id": resp.get("message", ""),
            "name": name,
            "model_path": model_path,
            "gpu_label": gpu_label,
            "gpu_count": defaults["nvidiaGpu"],
            "engine": f"{engine} {engine_version}",
            "status": "created",
        }
    else:
        raise RuntimeError(f"Create failed: HTTP {status}, response: {resp}")


def list_services(
    username: str | None = None,
    password: str | None = None,
    headless: bool = True,
) -> list[ServiceInfo]:
    """List all large-model services."""
    token = get_token(username, password, headless=headless)
    status, resp = _api_request(token, "GET", API_INFERENCE_LARGE)

    if not isinstance(resp, list):
        resp = resp if isinstance(resp, dict) else {}
        items = resp.get("items", resp.get("data", []))
    else:
        items = resp

    services = []
    for item in items:
        entity = item.get("entity", item)
        status_list = item.get("status", [])
        # Pick latest status message
        latest_status = ""
        if status_list and isinstance(status_list, list):
            phases = {s.get("phase") for s in status_list if isinstance(s, dict)}
            messages = [
                s.get("message")
                for s in status_list
                if isinstance(s, dict) and s.get("message")
            ]
            if 2 in phases:
                latest_status = "Running"
            elif 1 in phases:
                latest_status = "Deploying"
            elif messages:
                latest_status = messages[-1]

        # aliases is nested in entity.request.aliases
        request = entity.get("request", {})
        info = request.get("info", entity.get("info", {}))
        resources = entity.get("request", {}).get("resources", {})

        svc = ServiceInfo(
            id=entity.get("id", entity.get("name", "")),
            name=request.get("aliases", entity.get("aliases", entity.get("name", ""))),
            model_path=info.get("model_path", entity.get("model_dir", "")),
            gpu_label=resources.get("labels", ""),
            gpu_count=resources.get("resources_per_service", {}).get("nvidiaGpu", 0),
            status=latest_status,
            raw=item,
        )
        services.append(svc)
    return services


def get_service_status(
    service_id: str,
    username: str | None = None,
    password: str | None = None,
    headless: bool = True,
) -> ServiceInfo | None:
    """Get status of a specific service."""
    for svc in list_services(username, password, headless=headless):
        if svc.id == service_id or service_id in svc.id:
            return svc
    return None
