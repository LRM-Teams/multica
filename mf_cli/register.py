"""Register a Modelfactory inference service as a model in Leagent."""

import json
import urllib.error
import urllib.request
from dataclasses import dataclass
from pathlib import Path

from .auth import get_token
from .config import BASE_URL
from .service import list_services


def _load_env() -> dict[str, str]:
    """Read Leagent-related entries from the .env file next to this module."""
    env_file = Path(__file__).parent / ".env"
    if not env_file.exists():
        return {}
    values: dict[str, str] = {}
    for line in env_file.read_text().splitlines():
        line = line.strip()
        if "=" in line and not line.startswith("#"):
            key, _, val = line.partition("=")
            key = key.strip()
            val = val.strip().strip('"').strip("'")
            if key in (
                "LEAGENT_URL",
                "SUPABASE_URL",
                "SUPABASE_ANON_KEY",
                "LEAGENT_ADMIN_EMAIL",
                "LEAGENT_ADMIN_PASSWORD",
            ):
                values[key] = val
    return values


@dataclass
class ServiceEndpoint:
    """Endpoint information for a Modelfactory inference service."""

    service_id: str
    service_name: str
    model_name: str
    model_path: str
    endpoint_url: str
    api_key: str
    status: str


def _get_supabase_session(
    supabase_url: str, anon_key: str, email: str, password: str
) -> str:
    """Sign in to Supabase and return the access_token (JWT)."""
    url = f"{supabase_url}/auth/v1/token?grant_type=password"
    data = json.dumps({"email": email, "password": password}).encode("utf-8")
    req = urllib.request.Request(url, data=data, method="POST")
    req.add_header("apikey", anon_key)
    req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=15) as resp:
            body = json.loads(resp.read().decode())
            token = body.get("access_token", "")
            if not token:
                raise RuntimeError(f"Supabase login returned no access_token: {body}")
            return token
    except urllib.error.HTTPError as e:
        raw = e.read().decode()
        raise RuntimeError(f"Supabase login failed (HTTP {e.code}): {raw}") from e


def get_service_endpoint(
    service_id: str,
    username: str | None = None,
    password: str | None = None,
    headless: bool = True,
) -> ServiceEndpoint:
    """Get the inference endpoint URL and API key for a Modelfactory service.

    Queries the service list from Modelfactory and extracts endpoint details.
    The API key is the aimaster token by default (used for Modelfactory-hosted
    inference). If the service has a dedicated token in ``app_info.token`` it
    will be preferred.

    Returns:
        ServiceEndpoint with url, api_key, and model info.
    """
    token = get_token(username, password, headless=headless)
    services = list_services(username=username, password=password, headless=headless)

    svc = None
    for s in services:
        if s.id == service_id or service_id in s.id:
            svc = s
            break

    if not svc:
        raise ValueError(
            f"Service '{service_id}' not found. "
            f"Available services: {[s.id for s in services]}"
        )

    # The inference endpoint URL follows the standard Modelfactory pattern
    endpoint_url = f"{BASE_URL}/{svc.id}/llm/v1"

    # Prefer dedicated service token, fall back to aimaster token
    entity = svc.raw.get("entity", {})
    app_info = entity.get("app_info", {})
    api_key = app_info.get("token", "") or token  # empty string → use aimaster token

    model_name = svc.name or svc.id
    model_path = svc.model_path

    return ServiceEndpoint(
        service_id=svc.id,
        service_name=svc.name,
        model_name=model_name,
        model_path=model_path,
        endpoint_url=endpoint_url,
        api_key=api_key,
        status=svc.status,
    )


def register_model_in_leagent(
    endpoint: ServiceEndpoint,
    leagent_url: str,
    supabase_url: str,
    anon_key: str,
    admin_email: str,
    admin_password: str,
    provider: str = "openai-compatible",
    display_name: str | None = None,
    max_output_tokens: int = 16384,
    context_window: int = 131072,
    capabilities: list[str] | None = None,
    description: str | None = None,
) -> dict:
    """Register a service as a model in Leagent's admin API.

    Logs into Leagent via Supabase auth, then POSTs to ``/admin/llm-models``.

    Args:
        endpoint: ServiceEndpoint from :func:`get_service_endpoint`.
        leagent_url: Leagent backend URL, e.g. ``http://10.110.158.146:8000``.
        supabase_url: Supabase URL for Leagent auth.
        anon_key: Supabase anon key.
        admin_email: Leagent admin email.
        admin_password: Leagent admin password.
        provider: Model provider identifier.
        display_name: Human-readable name (defaults to endpoint.model_name).
        max_output_tokens: Max tokens per response.
        context_window: Max context window size.
        capabilities: List of model capabilities.
        description: Optional description.

    Returns:
        Parsed JSON response from the Leagent API.
    """
    jwt = _get_supabase_session(supabase_url, anon_key, admin_email, admin_password)

    body = {
        "model_name": endpoint.model_name,
        "api_key": endpoint.api_key,
        "status": "active",
        "config": {
            "provider": provider,
            "api_base": endpoint.endpoint_url,
            "verify_ssl": True,
            "display_name": display_name or endpoint.model_name,
            "max_output_tokens": max_output_tokens,
            "context_window": context_window,
            "capabilities": capabilities or [],
            "reasoning_efforts": [],
        },
    }
    if description:
        body["description"] = description

    url = f"{leagent_url.rstrip('/')}/api/admin/llm-models"
    data = json.dumps(body).encode("utf-8")
    req = urllib.request.Request(url, data=data, method="POST")
    req.add_header("Authorization", f"Bearer {jwt}")
    req.add_header("Content-Type", "application/json")

    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            return json.loads(resp.read().decode())
    except urllib.error.HTTPError as e:
        raw = e.read().decode()
        raise RuntimeError(
            f"Leagent model registration failed (HTTP {e.code}): {raw}"
        ) from e


def edit_model_in_leagent(
    model_identifier: str,
    leagent_url: str,
    supabase_url: str,
    anon_key: str,
    admin_email: str,
    admin_password: str,
    api_base: str | None = None,
    api_key: str | None = None,
) -> dict:
    """Edit an existing model's URL and/or API key in Leagent.

    Looks up the model by name or ID, then updates its ``api_base`` and/or
    ``api_key`` via the Leagent admin API.

    Args:
        model_identifier: Model ``model_name`` or UUID ``id`` to edit.
        leagent_url: Leagent backend URL, e.g. ``http://10.110.158.146:8000``.
        supabase_url: Supabase URL for Leagent auth.
        anon_key: Supabase anon key.
        admin_email: Leagent admin email.
        admin_password: Leagent admin password.
        api_base: New inference endpoint URL (optional).
        api_key: New API key (optional).

    Returns:
        Parsed JSON response from the last Leagent API call.

    Raises:
        ValueError: If neither ``api_base`` nor ``api_key`` is provided, or the
                    model cannot be found.
        RuntimeError: On API errors.
    """
    if not api_base and not api_key:
        raise ValueError("At least one of --url or --key must be provided")

    jwt = _get_supabase_session(supabase_url, anon_key, admin_email, admin_password)

    # List models and find the target by ID or model_name
    list_url = f"{leagent_url.rstrip('/')}/api/admin/llm-models"
    req = urllib.request.Request(list_url, method="GET")
    req.add_header("Authorization", f"Bearer {jwt}")
    req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            models = json.loads(resp.read().decode())
    except urllib.error.HTTPError as e:
        raw = e.read().decode()
        raise RuntimeError(
            f"Failed to list Leagent models (HTTP {e.code}): {raw}"
        ) from e

    target = None
    for m in models:
        if m["id"] == model_identifier or m.get("model_name") == model_identifier:
            if target is not None:
                raise ValueError(
                    f"Multiple models match '{model_identifier}'. "
                    f"Use the model ID instead."
                )
            target = m

    if not target:
        available = [m.get("model_name", m["id"]) for m in models]
        raise ValueError(
            f"Model '{model_identifier}' not found in Leagent. "
            f"Available models: {available}"
        )

    model_id = target["id"]
    last_result: dict = target

    # Update api_base (PATCH the full config with the new URL merged in)
    if api_base:
        config = dict(target.get("config", {}))
        config["api_base"] = api_base
        patch_body = {"config": config}
        patch_url = f"{leagent_url.rstrip('/')}/api/admin/llm-models/{model_id}"
        data = json.dumps(patch_body).encode("utf-8")
        req = urllib.request.Request(patch_url, data=data, method="PATCH")
        req.add_header("Authorization", f"Bearer {jwt}")
        req.add_header("Content-Type", "application/json")
        try:
            with urllib.request.urlopen(req, timeout=30) as resp:
                last_result = json.loads(resp.read().decode())
        except urllib.error.HTTPError as e:
            raw = e.read().decode()
            raise RuntimeError(
                f"Failed to update model URL (HTTP {e.code}): {raw}"
            ) from e

    # Rotate API key
    if api_key:
        key_body = {"api_key": api_key}
        key_url = (
            f"{leagent_url.rstrip('/')}/api/admin/llm-models/{model_id}/rotate-key"
        )
        data = json.dumps(key_body).encode("utf-8")
        req = urllib.request.Request(key_url, data=data, method="POST")
        req.add_header("Authorization", f"Bearer {jwt}")
        req.add_header("Content-Type", "application/json")
        try:
            with urllib.request.urlopen(req, timeout=30) as resp:
                last_result = json.loads(resp.read().decode())
        except urllib.error.HTTPError as e:
            raw = e.read().decode()
            raise RuntimeError(
                f"Failed to rotate model key (HTTP {e.code}): {raw}"
            ) from e

    return last_result


def delete_model_in_leagent(
    model_identifier: str,
    leagent_url: str,
    supabase_url: str,
    anon_key: str,
    admin_email: str,
    admin_password: str,
) -> dict:
    """Delete a model from Leagent by name or ID.

    Looks up the model by name or ID, then sends a ``DELETE`` request to the
    Leagent admin API.

    Args:
        model_identifier: Model ``model_name`` or UUID ``id`` to delete.
        leagent_url: Leagent backend URL, e.g. ``http://10.110.158.146:8000``.
        supabase_url: Supabase URL for Leagent auth.
        anon_key: Supabase anon key.
        admin_email: Leagent admin email.
        admin_password: Leagent admin password.

    Returns:
        Dict with ``model_name`` and ``id`` of the deleted model.

    Raises:
        ValueError: If the model cannot be found.
        RuntimeError: On API errors.
    """
    jwt = _get_supabase_session(supabase_url, anon_key, admin_email, admin_password)

    # List models and find the target by ID or model_name
    list_url = f"{leagent_url.rstrip('/')}/api/admin/llm-models"
    req = urllib.request.Request(list_url, method="GET")
    req.add_header("Authorization", f"Bearer {jwt}")
    req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            models = json.loads(resp.read().decode())
    except urllib.error.HTTPError as e:
        raw = e.read().decode()
        raise RuntimeError(
            f"Failed to list Leagent models (HTTP {e.code}): {raw}"
        ) from e

    target = None
    for m in models:
        if m["id"] == model_identifier or m.get("model_name") == model_identifier:
            if target is not None:
                raise ValueError(
                    f"Multiple models match '{model_identifier}'. "
                    f"Use the model ID instead."
                )
            target = m

    if not target:
        available = [m.get("model_name", m["id"]) for m in models]
        raise ValueError(
            f"Model '{model_identifier}' not found in Leagent. "
            f"Available models: {available}"
        )

    model_id = target["id"]
    model_name = target.get("model_name", model_id)

    delete_url = f"{leagent_url.rstrip('/')}/api/admin/llm-models/{model_id}"
    req = urllib.request.Request(delete_url, method="DELETE")
    req.add_header("Authorization", f"Bearer {jwt}")
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            pass  # 204 No Content — nothing to read
    except urllib.error.HTTPError as e:
        raw = e.read().decode()
        raise RuntimeError(f"Failed to delete model (HTTP {e.code}): {raw}") from e

    return {"id": model_id, "model_name": model_name}
