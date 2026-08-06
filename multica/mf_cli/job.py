"""Modelfactory job operations — create, list, status."""

import json
import urllib.request
import urllib.error
from dataclasses import dataclass, field
from typing import Any

from .auth import get_token
from .config import (
    BASE_URL, API_JOB_LIST, API_JOB_CREATE, API_BILLING_PRICE,
    DEFAULT_WORKSPACE_IMAGE, GPU_SPECS,
)


@dataclass
class JobInfo:
    """Information about a Modelfactory job."""
    id: str = ""
    name: str = ""
    image: str = ""
    gpu_label: str = ""
    gpu_count: int = 0
    command: list[str] = field(default_factory=list)
    status: str = ""
    raw: dict = field(default_factory=dict)


def _api_request(
    token: str, method: str, path: str, body: dict | None = None,
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


def _get_price_token(
    token: str, gpu_label: str, cpu: int, memory_gb: int, gpu_count: int,
) -> str:
    """Get a billing price token for job resources."""
    status, resp = _api_request(
        token, "POST", API_BILLING_PRICE,
        {
            "items": [
                {"label": "CPU", "num": cpu},
                {"label": "MEMORY", "num": memory_gb},
                {"label": gpu_label, "num": gpu_count},
            ],
            "isPt": False,
        },
    )
    if status != 200:
        raise RuntimeError(f"Failed to get price token: {resp}")
    return resp.get("price_token", "")


def create_job(
    username: str | None = None,
    password: str | None = None,
    name: str = "newjob",
    command: list[str] | None = None,
    image: str | None = None,
    gpu_label: str = "A800-8",
    gpu_count: int = 1,
    cpu: int = 32,
    memory_gb: int = 250,
    disk_gb: int = 2,
    instances: int = 1,
    headless: bool = True,
    **kwargs,
) -> dict:
    """Create a job on Modelfactory.

    Args:
        username: Modelfactory username
        password: Modelfactory password
        name: Job name / alias
        command: Command to run (list of strings, e.g. ['sh', '/path/to/script.sh'])
        image: Docker workspace image (auto-detected if None)
        gpu_label: GPU spec label, e.g. 'A800-8'
        gpu_count: Number of GPUs
        cpu: CPU cores
        memory_gb: Memory in GiB
        disk_gb: Disk in GiB
        instances: Number of instances
        headless: Run browser headless during login
    """
    token = get_token(username, password, headless=headless)

    if image is None:
        image = DEFAULT_WORKSPACE_IMAGE
    if command is None:
        command = ["sh"]

    memory_mib = memory_gb * 1024
    price_token = _get_price_token(token, gpu_label, cpu, memory_gb, gpu_count)

    body = {
        "aliases": name,
        "image": image,
        "labels": gpu_label,
        "allowed_labels": [gpu_label],
        "bash": "sh",
        "command": command,
        "resources": {
            "cpu": cpu,
            "nvidiaGpu": gpu_count,
            "gpu": 0,
            "memoryInMi": memory_mib,
            "diskInGi": disk_gb,
            "priceToken": price_token,
        },
        "instances": instances,
        "preemptible": False,
        "work_dir": "",
        "envs": None,
        "log_to": "",
    }

    status, resp = _api_request(token, "PUT", API_JOB_CREATE, body)

    if status == 200 and resp.get("code") == "S0000":
        return {
            "job_id": resp.get("message", ""),
            "name": name,
            "command": command,
            "gpu_label": gpu_label,
            "gpu_count": gpu_count,
            "cpu": cpu,
            "memory_gb": memory_gb,
            "status": "created",
        }
    else:
        raise RuntimeError(f"Create failed: HTTP {status}, response: {resp}")


def list_jobs(
    username: str | None = None,
    password: str | None = None,
    headless: bool = True,
) -> list[JobInfo]:
    """List all jobs."""
    token = get_token(username, password, headless=headless)
    status, resp = _api_request(token, "GET", API_JOB_LIST)

    if isinstance(resp, dict):
        items = resp.get("items", resp.get("data", []))
    else:
        items = resp if isinstance(resp, list) else []

    jobs = []
    for item in items:
        entity = item.get("entity", item)
        stat_list = item.get("status", [])
        # Determine status from phases
        phases = {s.get("phase") for s in stat_list if isinstance(s, dict)}
        if 2 in phases:
            latest_status = "Running"
        elif 1 in phases:
            latest_status = "Deploying"
        elif phases:
            latest_status = f"phase:{max(phases)}"
        else:
            latest_status = ""

        request = entity.get("request", {})
        resources = request.get("resources", {})

        j = JobInfo(
            id=entity.get("id", ""),
            name=request.get("aliases", ""),
            image=request.get("image", entity.get("image", ""))[:80],
            gpu_label=request.get("labels", ""),
            gpu_count=resources.get("nvidiaGpu", 0),
            command=request.get("command", []),
            status=latest_status,
            raw=item,
        )
        jobs.append(j)
    return jobs


def get_job_status(
    job_id: str,
    username: str | None = None,
    password: str | None = None,
    headless: bool = True,
) -> JobInfo | None:
    """Get status of a specific job."""
    for job in list_jobs(username, password, headless=headless):
        if job.id == job_id or job_id in job.id:
            return job
    return None
