"""Environment-driven configuration for the DB bridge.

All knobs are read from environment variables so the stub and executor can run
as standalone processes on each host. ``BridgeConfig.from_env`` validates the
required Supabase settings and applies sensible defaults for everything else.
"""

from __future__ import annotations

import os
import uuid
from collections.abc import Mapping
from dataclasses import dataclass, field
from typing import Final

from . import channels as _channels
from .channels import Channel, Group, Side

# Required
ENV_SUPABASE_URL: Final = "SUPABASE_URL"
ENV_SUPABASE_SERVICE_KEY: Final = "SUPABASE_SERVICE_ROLE_KEY"

# Polling / lifecycle
ENV_POLL_INTERVAL: Final = "BRIDGE_POLL_INTERVAL"
ENV_STALE_SECONDS: Final = "BRIDGE_STALE_SECONDS"
ENV_BRIDGE_USER_ID: Final = "BRIDGE_USER_ID"

# Codec / size guard
ENV_CODEC_THRESHOLD: Final = "BRIDGE_CODEC_THRESHOLD"
ENV_MAX_BODY_BYTES: Final = "BRIDGE_MAX_BODY_BYTES"

# Stub server
ENV_GATEWAY_STUB_PORT: Final = "BRIDGE_GATEWAY_STUB_PORT"
ENV_LEAGENT_STUB_PORT: Final = "BRIDGE_LEAGENT_STUB_PORT"
ENV_STUB_HOST: Final = "BRIDGE_STUB_HOST"

# Executor upstreams (real local services, reached over loopback).
# Plural forms (URLS) hold a comma-separated list for multi-port failover;
# singular forms (URL) are accepted as a single-element fallback.
ENV_GATEWAY_UPSTREAMS: Final = "BRIDGE_GATEWAY_UPSTREAM_URLS"
ENV_GATEWAY_UPSTREAM: Final = "BRIDGE_GATEWAY_UPSTREAM_URL"
ENV_LEAGENT_UPSTREAMS: Final = "BRIDGE_LEAGENT_UPSTREAM_URLS"
ENV_LEAGENT_UPSTREAM: Final = "BRIDGE_LEAGENT_UPSTREAM_URL"
ENV_MULTICA_UPSTREAM: Final = "BRIDGE_MULTICA_UPSTREAM_URL"

# Security hardening
ENV_REDACT_TOKENS: Final = "BRIDGE_REDACT_TOKENS_AFTER_COMPLETE"
ENV_HEADER_ENCRYPTION_KEY: Final = "BRIDGE_HEADER_ENCRYPTION_KEY"
ENV_ADMIN_API_KEY: Final = "KORTIX_ADMIN_API_KEY"

# Observability
ENV_STATS_INTERVAL: Final = "BRIDGE_STATS_INTERVAL"
ENV_CLEANUP_INTERVAL: Final = "BRIDGE_CLEANUP_INTERVAL"
ENV_ROW_RETENTION_SECONDS: Final = "BRIDGE_ROW_RETENTION_SECONDS"
ENV_CLEANUP_BATCH_LIMIT: Final = "BRIDGE_CLEANUP_BATCH_LIMIT"

# Streaming (SSE chat completions relayed through bridge_stream_chunks)
ENV_STREAM_FIRST_CHUNK_TIMEOUT: Final = "BRIDGE_STREAM_FIRST_CHUNK_TIMEOUT"
ENV_STREAM_INTER_CHUNK_TIMEOUT: Final = "BRIDGE_STREAM_INTER_CHUNK_TIMEOUT"
ENV_STREAM_POLL_INTERVAL: Final = "BRIDGE_STREAM_POLL_INTERVAL"
ENV_STREAM_FLUSH_BYTES: Final = "BRIDGE_STREAM_FLUSH_BYTES"
ENV_STREAM_FLUSH_INTERVAL: Final = "BRIDGE_STREAM_FLUSH_INTERVAL"
ENV_STREAM_SWEEP_INTERVAL: Final = "BRIDGE_STREAM_SWEEP_INTERVAL"
ENV_STREAM_CHUNK_RETENTION_SECONDS: Final = "BRIDGE_STREAM_CHUNK_RETENTION_SECONDS"

_DEFAULT_POLL_INTERVAL: Final = 0.075
_DEFAULT_STALE_SECONDS: Final = 300
_DEFAULT_CODEC_THRESHOLD: Final = 2048
_DEFAULT_MAX_BODY_BYTES: Final = 64 * 1024 * 1024  # 64 MiB
_DEFAULT_GATEWAY_STUB_PORT: Final = 9100
_DEFAULT_LEAGENT_STUB_PORT: Final = 9101
_DEFAULT_STUB_HOST: Final = "127.0.0.1"
_DEFAULT_GATEWAY_UPSTREAM: Final = "http://127.0.0.1:8080"
_DEFAULT_LEAGENT_UPSTREAM: Final = "http://127.0.0.1:8000"
# The multica Go server's default $PORT is 8080 (server/cmd/server/main.go).
# Same loopback port as the AReaL gateway default, but on a DIFFERENT host
# (multica host vs areal host), so there is no collision.
_DEFAULT_MULTICA_UPSTREAM: Final = "http://127.0.0.1:8080"
_DEFAULT_STATS_INTERVAL: Final = 0.0
_DEFAULT_CLEANUP_INTERVAL: Final = 300.0
_DEFAULT_ROW_RETENTION_SECONDS: Final = 24 * 60 * 60
_DEFAULT_CLEANUP_BATCH_LIMIT: Final = 1000

# Streaming defaults. A 1s poll interval caps DB read load per active stream
# (~1 QPS instead of ~20 QPS at 50ms); FLUSH_BYTES=0 flushes on every complete
# SSE event boundary, and FLUSH_INTERVAL stays small so write-side latency is
# unaffected.
_DEFAULT_STREAM_FIRST_CHUNK_TIMEOUT: Final = 60.0
_DEFAULT_STREAM_INTER_CHUNK_TIMEOUT: Final = 120.0
_DEFAULT_STREAM_POLL_INTERVAL: Final = 1.0
_DEFAULT_STREAM_FLUSH_BYTES: Final = 0
_DEFAULT_STREAM_FLUSH_INTERVAL: Final = 0.05
_DEFAULT_STREAM_SWEEP_INTERVAL: Final = 30.0
_DEFAULT_STREAM_CHUNK_RETENTION_SECONDS: Final = 24 * 60 * 60


def _get(src: Mapping[str, str], name: str) -> str | None:
    val = src.get(name)
    return val if val is not None and val.strip() else None


def _parse_upstream_urls(
    src: Mapping[str, str],
    plural_name: str,
    singular_name: str,
    default: str,
) -> tuple[str, ...]:
    """Parse a comma-separated list of upstream URLs.

    Reads the plural env var (e.g. ``BRIDGE_GATEWAY_UPSTREAM_URLS``) first,
    falling back to the singular form (``BRIDGE_GATEWAY_UPSTREAM_URL``) for
    backward compatibility, then to *default*. Trailing slashes and blank
    entries are stripped; the result is always non-empty.
    """
    raw = _get(src, plural_name) or _get(src, singular_name) or default
    urls = tuple(u.strip().rstrip("/") for u in raw.split(","))
    urls = tuple(u for u in urls if u)
    if not urls:
        return (default.rstrip("/"),)
    return urls


def _env_float(src: Mapping[str, str], name: str, default: float) -> float:
    raw = _get(src, name)
    if raw is None:
        return default
    try:
        return float(raw)
    except ValueError as exc:
        raise ValueError(f"{name} must be a number, got {raw!r}") from exc


def _env_int(src: Mapping[str, str], name: str, default: int) -> int:
    raw = _get(src, name)
    if raw is None:
        return default
    try:
        return int(raw)
    except ValueError as exc:
        raise ValueError(f"{name} must be an integer, got {raw!r}") from exc


def _env_bool(src: Mapping[str, str], name: str, default: bool = False) -> bool:
    raw = _get(src, name)
    if raw is None:
        return default
    return raw.strip().lower() in {"1", "true", "yes", "on"}


def _require_positive(name: str, value: int | float) -> None:
    if value <= 0:
        raise ValueError(f"{name} must be greater than zero, got {value!r}")


def _require_non_negative(name: str, value: int | float) -> None:
    if value < 0:
        raise ValueError(f"{name} must be zero or greater, got {value!r}")


def _env_uuid(src: Mapping[str, str], name: str) -> str | None:
    raw = _get(src, name)
    if raw is None:
        return None
    try:
        return str(uuid.UUID(raw.strip()))
    except ValueError as exc:
        raise ValueError(f"{name} must be a valid UUID, got {raw!r}") from exc


@dataclass(frozen=True, slots=True)
class BridgeConfig:
    """Resolved bridge configuration."""

    supabase_url: str
    supabase_key: str
    poll_interval_s: float = _DEFAULT_POLL_INTERVAL
    stale_seconds: int = _DEFAULT_STALE_SECONDS
    codec_threshold: int = _DEFAULT_CODEC_THRESHOLD
    max_body_bytes: int = _DEFAULT_MAX_BODY_BYTES
    stub_host: str = _DEFAULT_STUB_HOST
    gateway_stub_port: int = _DEFAULT_GATEWAY_STUB_PORT
    leagent_stub_port: int = _DEFAULT_LEAGENT_STUB_PORT
    gateway_upstream_url: str = _DEFAULT_GATEWAY_UPSTREAM
    gateway_upstream_urls: tuple[str, ...] = (_DEFAULT_GATEWAY_UPSTREAM,)
    leagent_upstream_url: str = _DEFAULT_LEAGENT_UPSTREAM
    leagent_upstream_urls: tuple[str, ...] = (_DEFAULT_LEAGENT_UPSTREAM,)
    multica_upstream_url: str = _DEFAULT_MULTICA_UPSTREAM
    redact_tokens_after_complete: bool = False
    header_encryption_key: str | None = None
    admin_api_key: str | None = None
    stats_interval_s: float = _DEFAULT_STATS_INTERVAL
    cleanup_interval_s: float = _DEFAULT_CLEANUP_INTERVAL
    row_retention_seconds: int = _DEFAULT_ROW_RETENTION_SECONDS
    cleanup_batch_limit: int = _DEFAULT_CLEANUP_BATCH_LIMIT
    # Streaming (SSE) relay knobs.
    stream_first_chunk_timeout_s: float = _DEFAULT_STREAM_FIRST_CHUNK_TIMEOUT
    stream_inter_chunk_timeout_s: float = _DEFAULT_STREAM_INTER_CHUNK_TIMEOUT
    stream_poll_interval_s: float = _DEFAULT_STREAM_POLL_INTERVAL
    stream_flush_bytes: int = _DEFAULT_STREAM_FLUSH_BYTES
    stream_flush_interval_s: float = _DEFAULT_STREAM_FLUSH_INTERVAL
    stream_sweep_interval_s: float = _DEFAULT_STREAM_SWEEP_INTERVAL
    stream_chunk_retention_seconds: int = _DEFAULT_STREAM_CHUNK_RETENTION_SECONDS
    bridge_user_id: str | None = None
    # Per-channel overrides, keyed by channel name.
    timeout_overrides: dict[str, float] = field(default_factory=dict)
    concurrency_overrides: dict[str, int] = field(default_factory=dict)

    @classmethod
    def from_env(cls, env: Mapping[str, str] | None = None) -> BridgeConfig:
        src: Mapping[str, str] = os.environ if env is None else env

        supabase_url = _get(src, ENV_SUPABASE_URL)
        supabase_key = _get(src, ENV_SUPABASE_SERVICE_KEY)
        if not supabase_url or not supabase_key:
            raise RuntimeError(
                f"{ENV_SUPABASE_URL} and {ENV_SUPABASE_SERVICE_KEY} must be set."
            )

        # Per-channel overrides via BRIDGE_TIMEOUT_<NAME> /
        # BRIDGE_CONCURRENCY_<NAME> (channel name upper-cased).
        timeout_overrides: dict[str, float] = {}
        concurrency_overrides: dict[str, int] = {}
        for channel in _channels.CHANNELS:
            t_raw = _get(src, f"BRIDGE_TIMEOUT_{channel.name.upper()}")
            c_raw = _get(src, f"BRIDGE_CONCURRENCY_{channel.name.upper()}")
            if t_raw is not None:
                timeout_overrides[channel.name] = float(t_raw)
                _require_positive(
                    f"BRIDGE_TIMEOUT_{channel.name.upper()}",
                    timeout_overrides[channel.name],
                )
            if c_raw is not None:
                concurrency_overrides[channel.name] = int(c_raw)
                _require_positive(
                    f"BRIDGE_CONCURRENCY_{channel.name.upper()}",
                    concurrency_overrides[channel.name],
                )

        poll_interval_s = _env_float(src, ENV_POLL_INTERVAL, _DEFAULT_POLL_INTERVAL)
        stale_seconds = _env_int(src, ENV_STALE_SECONDS, _DEFAULT_STALE_SECONDS)
        codec_threshold = _env_int(src, ENV_CODEC_THRESHOLD, _DEFAULT_CODEC_THRESHOLD)
        max_body_bytes = _env_int(src, ENV_MAX_BODY_BYTES, _DEFAULT_MAX_BODY_BYTES)
        gateway_stub_port = _env_int(
            src, ENV_GATEWAY_STUB_PORT, _DEFAULT_GATEWAY_STUB_PORT
        )
        leagent_stub_port = _env_int(
            src, ENV_LEAGENT_STUB_PORT, _DEFAULT_LEAGENT_STUB_PORT
        )
        stats_interval_s = _env_float(src, ENV_STATS_INTERVAL, _DEFAULT_STATS_INTERVAL)
        cleanup_interval_s = _env_float(
            src, ENV_CLEANUP_INTERVAL, _DEFAULT_CLEANUP_INTERVAL
        )
        row_retention_seconds = _env_int(
            src, ENV_ROW_RETENTION_SECONDS, _DEFAULT_ROW_RETENTION_SECONDS
        )
        cleanup_batch_limit = _env_int(
            src, ENV_CLEANUP_BATCH_LIMIT, _DEFAULT_CLEANUP_BATCH_LIMIT
        )

        stream_first_chunk_timeout_s = _env_float(
            src, ENV_STREAM_FIRST_CHUNK_TIMEOUT, _DEFAULT_STREAM_FIRST_CHUNK_TIMEOUT
        )
        stream_inter_chunk_timeout_s = _env_float(
            src, ENV_STREAM_INTER_CHUNK_TIMEOUT, _DEFAULT_STREAM_INTER_CHUNK_TIMEOUT
        )
        stream_poll_interval_s = _env_float(
            src, ENV_STREAM_POLL_INTERVAL, _DEFAULT_STREAM_POLL_INTERVAL
        )
        stream_flush_bytes = _env_int(
            src, ENV_STREAM_FLUSH_BYTES, _DEFAULT_STREAM_FLUSH_BYTES
        )
        stream_flush_interval_s = _env_float(
            src, ENV_STREAM_FLUSH_INTERVAL, _DEFAULT_STREAM_FLUSH_INTERVAL
        )
        stream_sweep_interval_s = _env_float(
            src, ENV_STREAM_SWEEP_INTERVAL, _DEFAULT_STREAM_SWEEP_INTERVAL
        )
        stream_chunk_retention_seconds = _env_int(
            src,
            ENV_STREAM_CHUNK_RETENTION_SECONDS,
            _DEFAULT_STREAM_CHUNK_RETENTION_SECONDS,
        )

        _require_positive(ENV_POLL_INTERVAL, poll_interval_s)
        _require_positive(ENV_STALE_SECONDS, stale_seconds)
        _require_non_negative(ENV_CODEC_THRESHOLD, codec_threshold)
        _require_positive(ENV_MAX_BODY_BYTES, max_body_bytes)
        _require_positive(ENV_GATEWAY_STUB_PORT, gateway_stub_port)
        _require_positive(ENV_LEAGENT_STUB_PORT, leagent_stub_port)
        _require_non_negative(ENV_STATS_INTERVAL, stats_interval_s)
        _require_non_negative(ENV_CLEANUP_INTERVAL, cleanup_interval_s)
        _require_positive(ENV_ROW_RETENTION_SECONDS, row_retention_seconds)
        _require_positive(ENV_CLEANUP_BATCH_LIMIT, cleanup_batch_limit)
        _require_positive(ENV_STREAM_FIRST_CHUNK_TIMEOUT, stream_first_chunk_timeout_s)
        _require_positive(ENV_STREAM_INTER_CHUNK_TIMEOUT, stream_inter_chunk_timeout_s)
        _require_positive(ENV_STREAM_POLL_INTERVAL, stream_poll_interval_s)
        _require_non_negative(ENV_STREAM_FLUSH_BYTES, stream_flush_bytes)
        _require_non_negative(ENV_STREAM_FLUSH_INTERVAL, stream_flush_interval_s)
        _require_non_negative(ENV_STREAM_SWEEP_INTERVAL, stream_sweep_interval_s)
        _require_positive(
            ENV_STREAM_CHUNK_RETENTION_SECONDS, stream_chunk_retention_seconds
        )

        gateway_urls = _parse_upstream_urls(
            src, ENV_GATEWAY_UPSTREAMS, ENV_GATEWAY_UPSTREAM, _DEFAULT_GATEWAY_UPSTREAM
        )
        leagent_urls = _parse_upstream_urls(
            src, ENV_LEAGENT_UPSTREAMS, ENV_LEAGENT_UPSTREAM, _DEFAULT_LEAGENT_UPSTREAM
        )

        return cls(
            supabase_url=supabase_url,
            supabase_key=supabase_key,
            poll_interval_s=poll_interval_s,
            stale_seconds=stale_seconds,
            codec_threshold=codec_threshold,
            max_body_bytes=max_body_bytes,
            stub_host=_get(src, ENV_STUB_HOST) or _DEFAULT_STUB_HOST,
            gateway_stub_port=gateway_stub_port,
            leagent_stub_port=leagent_stub_port,
            gateway_upstream_url=gateway_urls[0],
            gateway_upstream_urls=gateway_urls,
            leagent_upstream_url=leagent_urls[0],
            leagent_upstream_urls=leagent_urls,
            multica_upstream_url=(
                _get(src, ENV_MULTICA_UPSTREAM) or _DEFAULT_MULTICA_UPSTREAM
            ).rstrip("/"),
            redact_tokens_after_complete=_env_bool(src, ENV_REDACT_TOKENS, False),
            header_encryption_key=_get(src, ENV_HEADER_ENCRYPTION_KEY),
            admin_api_key=_get(src, ENV_ADMIN_API_KEY),
            stats_interval_s=stats_interval_s,
            cleanup_interval_s=cleanup_interval_s,
            row_retention_seconds=row_retention_seconds,
            cleanup_batch_limit=cleanup_batch_limit,
            stream_first_chunk_timeout_s=stream_first_chunk_timeout_s,
            stream_inter_chunk_timeout_s=stream_inter_chunk_timeout_s,
            stream_poll_interval_s=stream_poll_interval_s,
            stream_flush_bytes=stream_flush_bytes,
            stream_flush_interval_s=stream_flush_interval_s,
            stream_sweep_interval_s=stream_sweep_interval_s,
            stream_chunk_retention_seconds=stream_chunk_retention_seconds,
            bridge_user_id=_env_uuid(src, ENV_BRIDGE_USER_ID),
            timeout_overrides=timeout_overrides,
            concurrency_overrides=concurrency_overrides,
        )

    # -- per-channel resolution --------------------------------------------

    def timeout_for(self, channel: Channel) -> float:
        return self.timeout_overrides.get(channel.name, channel.default_timeout_s)

    def concurrency_for(self, channel: Channel) -> int:
        return self.concurrency_overrides.get(channel.name, channel.default_concurrency)

    def stub_port(self, side: Side) -> int:
        return self.gateway_stub_port if side == "multica" else self.leagent_stub_port

    def upstream_for_group(self, group: Group) -> str:
        if group == "gateway":
            return self.gateway_upstream_url
        if group == "multica_api":
            return self.multica_upstream_url
        return self.leagent_upstream_url

    def build_cipher(self, *, required: bool = False):
        """Build a header cipher when an encryption key is configured.

        Returns ``None`` when no key is set. Raises if a key is set but the
        optional ``cryptography`` package is unavailable.
        """
        if required and not self.header_encryption_key:
            raise RuntimeError(
                "BRIDGE_HEADER_ENCRYPTION_KEY must be set for multica_api channels"
            )
        from . import crypto

        return crypto.build_cipher(self.header_encryption_key)


# ---------------------------------------------------------------------------
# Remote shell runner configuration (AReaL host)
# ---------------------------------------------------------------------------

# Feature flag + identity
ENV_SHELL_ENABLED: Final = "AREAL_REMOTE_SHELL_ENABLED"
ENV_SHELL_RUNNER_ID: Final = "AREAL_REMOTE_SHELL_RUNNER_ID"

# Polling / lease lifecycle
ENV_SHELL_POLL_INTERVAL: Final = "AREAL_REMOTE_SHELL_POLL_INTERVAL"
ENV_SHELL_LEASE_SECONDS: Final = "AREAL_REMOTE_SHELL_LEASE_SECONDS"
ENV_SHELL_SWEEP_INTERVAL: Final = "AREAL_REMOTE_SHELL_SWEEP_INTERVAL"

# Command execution limits
ENV_SHELL_DEFAULT_TIMEOUT: Final = "AREAL_REMOTE_SHELL_DEFAULT_TIMEOUT"
ENV_SHELL_MAX_TIMEOUT: Final = "AREAL_REMOTE_SHELL_MAX_TIMEOUT"
ENV_SHELL_MAX_LOG_BYTES: Final = "AREAL_REMOTE_SHELL_MAX_LOG_BYTES"
ENV_SHELL_MAX_CONCURRENCY: Final = "AREAL_REMOTE_SHELL_MAX_CONCURRENCY"

# tmux / filesystem
ENV_SHELL_DEFAULT_CWD: Final = "AREAL_REMOTE_SHELL_DEFAULT_CWD"
ENV_SHELL_SESSION_PREFIX: Final = "AREAL_REMOTE_SHELL_SESSION_PREFIX"
ENV_SHELL_WORK_DIR: Final = "AREAL_REMOTE_SHELL_WORK_DIR"
ENV_SHELL_TMUX_BIN: Final = "AREAL_REMOTE_SHELL_TMUX_BIN"
ENV_SHELL_CLEANUP_INTERVAL: Final = "AREAL_REMOTE_SHELL_CLEANUP_INTERVAL"
ENV_SHELL_RETENTION_SECONDS: Final = "AREAL_REMOTE_SHELL_RETENTION_SECONDS"

_DEFAULT_SHELL_POLL_INTERVAL: Final = 1.0
_DEFAULT_SHELL_LEASE_SECONDS: Final = 60
_DEFAULT_SHELL_SWEEP_INTERVAL: Final = 30.0
_DEFAULT_SHELL_DEFAULT_TIMEOUT: Final = 300
_DEFAULT_SHELL_MAX_TIMEOUT: Final = 3600
_DEFAULT_SHELL_MAX_LOG_BYTES: Final = 64 * 1024  # 64 KiB tail per stream
_DEFAULT_SHELL_MAX_CONCURRENCY: Final = 4
_DEFAULT_SHELL_SESSION_PREFIX: Final = "areal_"
_DEFAULT_SHELL_WORK_DIR: Final = "/tmp/areal_remote_shell"  # noqa: S108 -- runner scratch
_DEFAULT_SHELL_TMUX_BIN: Final = "tmux"
_DEFAULT_SHELL_CLEANUP_INTERVAL: Final = 300.0
_DEFAULT_SHELL_RETENTION_SECONDS: Final = 7 * 24 * 60 * 60


@dataclass(frozen=True, slots=True)
class RemoteShellConfig:
    """Resolved configuration for the AReaL-host remote shell runner.

    The runner shares the bridge's Supabase service-role credentials but is an
    independent process with its own lifecycle knobs. ``enabled`` mirrors the
    backend feature flag: a runner started with the flag disabled refuses to
    claim commands, so an accidental deploy never executes host shell code.
    """

    supabase_url: str
    supabase_key: str
    enabled: bool = False
    runner_id: str = ""
    poll_interval_s: float = _DEFAULT_SHELL_POLL_INTERVAL
    lease_seconds: int = _DEFAULT_SHELL_LEASE_SECONDS
    sweep_interval_s: float = _DEFAULT_SHELL_SWEEP_INTERVAL
    default_timeout_s: int = _DEFAULT_SHELL_DEFAULT_TIMEOUT
    max_timeout_s: int = _DEFAULT_SHELL_MAX_TIMEOUT
    max_log_bytes: int = _DEFAULT_SHELL_MAX_LOG_BYTES
    max_concurrency: int = _DEFAULT_SHELL_MAX_CONCURRENCY
    default_cwd: str | None = None
    session_prefix: str = _DEFAULT_SHELL_SESSION_PREFIX
    work_dir: str = _DEFAULT_SHELL_WORK_DIR
    tmux_bin: str = _DEFAULT_SHELL_TMUX_BIN
    cleanup_interval_s: float = _DEFAULT_SHELL_CLEANUP_INTERVAL
    retention_seconds: int = _DEFAULT_SHELL_RETENTION_SECONDS

    @classmethod
    def from_env(cls, env: Mapping[str, str] | None = None) -> RemoteShellConfig:
        src: Mapping[str, str] = os.environ if env is None else env

        supabase_url = _get(src, ENV_SUPABASE_URL)
        supabase_key = _get(src, ENV_SUPABASE_SERVICE_KEY)
        if not supabase_url or not supabase_key:
            raise RuntimeError(
                f"{ENV_SUPABASE_URL} and {ENV_SUPABASE_SERVICE_KEY} must be set."
            )

        runner_id = _get(src, ENV_SHELL_RUNNER_ID) or f"shell-runner-{uuid.uuid4().hex}"

        poll_interval_s = _env_float(
            src, ENV_SHELL_POLL_INTERVAL, _DEFAULT_SHELL_POLL_INTERVAL
        )
        lease_seconds = _env_int(
            src, ENV_SHELL_LEASE_SECONDS, _DEFAULT_SHELL_LEASE_SECONDS
        )
        sweep_interval_s = _env_float(
            src, ENV_SHELL_SWEEP_INTERVAL, _DEFAULT_SHELL_SWEEP_INTERVAL
        )
        default_timeout_s = _env_int(
            src, ENV_SHELL_DEFAULT_TIMEOUT, _DEFAULT_SHELL_DEFAULT_TIMEOUT
        )
        max_timeout_s = _env_int(src, ENV_SHELL_MAX_TIMEOUT, _DEFAULT_SHELL_MAX_TIMEOUT)
        max_log_bytes = _env_int(
            src, ENV_SHELL_MAX_LOG_BYTES, _DEFAULT_SHELL_MAX_LOG_BYTES
        )
        max_concurrency = _env_int(
            src, ENV_SHELL_MAX_CONCURRENCY, _DEFAULT_SHELL_MAX_CONCURRENCY
        )
        cleanup_interval_s = _env_float(
            src, ENV_SHELL_CLEANUP_INTERVAL, _DEFAULT_SHELL_CLEANUP_INTERVAL
        )
        retention_seconds = _env_int(
            src, ENV_SHELL_RETENTION_SECONDS, _DEFAULT_SHELL_RETENTION_SECONDS
        )

        _require_positive(ENV_SHELL_POLL_INTERVAL, poll_interval_s)
        _require_positive(ENV_SHELL_LEASE_SECONDS, lease_seconds)
        _require_positive(ENV_SHELL_SWEEP_INTERVAL, sweep_interval_s)
        _require_positive(ENV_SHELL_DEFAULT_TIMEOUT, default_timeout_s)
        _require_positive(ENV_SHELL_MAX_TIMEOUT, max_timeout_s)
        _require_positive(ENV_SHELL_MAX_LOG_BYTES, max_log_bytes)
        _require_positive(ENV_SHELL_MAX_CONCURRENCY, max_concurrency)
        _require_non_negative(ENV_SHELL_CLEANUP_INTERVAL, cleanup_interval_s)
        _require_positive(ENV_SHELL_RETENTION_SECONDS, retention_seconds)

        if lease_seconds <= poll_interval_s:
            raise ValueError(
                f"{ENV_SHELL_LEASE_SECONDS} ({lease_seconds}) must exceed "
                f"{ENV_SHELL_POLL_INTERVAL} ({poll_interval_s}) so heartbeats can "
                "refresh the lease before it expires."
            )
        if default_timeout_s > max_timeout_s:
            raise ValueError(
                f"{ENV_SHELL_DEFAULT_TIMEOUT} ({default_timeout_s}) must not exceed "
                f"{ENV_SHELL_MAX_TIMEOUT} ({max_timeout_s})."
            )

        return cls(
            supabase_url=supabase_url,
            supabase_key=supabase_key,
            enabled=_env_bool(src, ENV_SHELL_ENABLED, False),
            runner_id=runner_id,
            poll_interval_s=poll_interval_s,
            lease_seconds=lease_seconds,
            sweep_interval_s=sweep_interval_s,
            default_timeout_s=default_timeout_s,
            max_timeout_s=max_timeout_s,
            max_log_bytes=max_log_bytes,
            max_concurrency=max_concurrency,
            default_cwd=_get(src, ENV_SHELL_DEFAULT_CWD),
            session_prefix=(
                _get(src, ENV_SHELL_SESSION_PREFIX) or _DEFAULT_SHELL_SESSION_PREFIX
            ),
            work_dir=_get(src, ENV_SHELL_WORK_DIR) or _DEFAULT_SHELL_WORK_DIR,
            tmux_bin=_get(src, ENV_SHELL_TMUX_BIN) or _DEFAULT_SHELL_TMUX_BIN,
            cleanup_interval_s=cleanup_interval_s,
            retention_seconds=retention_seconds,
        )

    def resolve_timeout(self, requested: int | None) -> int:
        """Clamp a per-command timeout into ``[1, max_timeout_s]``.

        ``None`` or a non-positive request falls back to ``default_timeout_s``.
        """
        if requested is None or requested <= 0:
            return self.default_timeout_s
        return min(requested, self.max_timeout_s)

    def session_name(self, tmux_id: str) -> str:
        """Stable tmux session name for a remote shell terminal."""
        return f"{self.session_prefix}{tmux_id}"


# ---------------------------------------------------------------------------
# Multica-side server configuration (public internet host)
# ---------------------------------------------------------------------------
#
# The multica server is the first *internet-exposed* bridge surface. It accepts
# OpenAI-compatible LLM calls and remote-shell enqueue requests from untrusted
# callers, authenticates them with static API keys, and parks the work in the
# shared Supabase database for the AReaL-side executor / shell runner to drain.
# multica cannot reach AReaL directly; every call is DB-routed.

# Default LLM relay timeout mirrors the shared chat_completions channel so the
# multica server waits as long as le-agent's gateway stub does by default.
_CHAT_COMPLETIONS_DEFAULT_TIMEOUT: Final = _channels.CHANNELS_BY_NAME[
    "chat_completions"
].default_timeout_s

# Identity + auth
ENV_MULTICA_BRIDGE_USER_ID: Final = "MULTICA_BRIDGE_USER_ID"
ENV_MULTICA_LLM_API_KEYS: Final = "MULTICA_LLM_API_KEYS"
ENV_MULTICA_SHELL_API_KEYS: Final = "MULTICA_SHELL_API_KEYS"

# Bind (kept behind the reverse proxy; compose maps 127.0.0.1:<port>)
ENV_MULTICA_BIND_HOST: Final = "MULTICA_BIND_HOST"
ENV_MULTICA_PORT: Final = "MULTICA_PORT"

# LLM relay
ENV_MULTICA_CHAT_TIMEOUT: Final = "MULTICA_CHAT_TIMEOUT"
# Optional auth replayed to the real AReaL gateway. When unset, the inbound
# multica API key is stripped and no Authorization is forwarded upstream.
ENV_MULTICA_UPSTREAM_API_KEY: Final = "MULTICA_UPSTREAM_API_KEY"

# Streaming (SSE) relay: caller-side timeouts + chunk poll cadence.
ENV_MULTICA_STREAM_FIRST_CHUNK_TIMEOUT: Final = "MULTICA_STREAM_FIRST_CHUNK_TIMEOUT"
ENV_MULTICA_STREAM_INTER_CHUNK_TIMEOUT: Final = "MULTICA_STREAM_INTER_CHUNK_TIMEOUT"
ENV_MULTICA_STREAM_POLL_INTERVAL: Final = "MULTICA_STREAM_POLL_INTERVAL"

# Remote-shell enqueue
ENV_MULTICA_SHELL_DEFAULT_TIMEOUT: Final = "MULTICA_SHELL_DEFAULT_TIMEOUT"
ENV_MULTICA_SHELL_MAX_TIMEOUT: Final = "MULTICA_SHELL_MAX_TIMEOUT"
ENV_MULTICA_SHELL_DEFAULT_CWD: Final = "MULTICA_SHELL_DEFAULT_CWD"

_DEFAULT_MULTICA_BIND_HOST: Final = "0.0.0.0"  # noqa: S104 -- bound behind reverse proxy
_DEFAULT_MULTICA_PORT: Final = 9200
_DEFAULT_MULTICA_SHELL_DEFAULT_TIMEOUT: Final = 300
_DEFAULT_MULTICA_SHELL_MAX_TIMEOUT: Final = 3600


def _split_keys(raw: str | None) -> frozenset[str]:
    """Parse a comma-separated key list into a set of trimmed, non-empty keys."""
    if not raw:
        return frozenset()
    return frozenset(part.strip() for part in raw.split(",") if part.strip())


@dataclass(frozen=True, slots=True)
class MulticaConfig:
    """Resolved configuration for the multica-side public bridge server.

    Shares the Supabase service-role credentials with the rest of the bridge but
    is an independent process with its own auth and bind knobs. Static keys are
    fail-closed: an empty key set rejects every request to that surface.
    """

    supabase_url: str
    supabase_key: str
    bridge_user_id: str
    llm_api_keys: frozenset[str] = frozenset()
    shell_api_keys: frozenset[str] = frozenset()
    bind_host: str = _DEFAULT_MULTICA_BIND_HOST
    port: int = _DEFAULT_MULTICA_PORT
    chat_timeout_s: float = _CHAT_COMPLETIONS_DEFAULT_TIMEOUT
    max_body_bytes: int = _DEFAULT_MAX_BODY_BYTES
    upstream_api_key: str | None = None
    shell_default_timeout_s: int = _DEFAULT_MULTICA_SHELL_DEFAULT_TIMEOUT
    shell_max_timeout_s: int = _DEFAULT_MULTICA_SHELL_MAX_TIMEOUT
    shell_default_cwd: str | None = None
    poll_interval_s: float = _DEFAULT_POLL_INTERVAL
    # Streaming (SSE) relay knobs (caller side).
    stream_first_chunk_timeout_s: float = _DEFAULT_STREAM_FIRST_CHUNK_TIMEOUT
    stream_inter_chunk_timeout_s: float = _DEFAULT_STREAM_INTER_CHUNK_TIMEOUT
    stream_poll_interval_s: float = _DEFAULT_STREAM_POLL_INTERVAL

    @classmethod
    def from_env(cls, env: Mapping[str, str] | None = None) -> MulticaConfig:
        src: Mapping[str, str] = os.environ if env is None else env

        supabase_url = _get(src, ENV_SUPABASE_URL)
        supabase_key = _get(src, ENV_SUPABASE_SERVICE_KEY)
        if not supabase_url or not supabase_key:
            raise RuntimeError(
                f"{ENV_SUPABASE_URL} and {ENV_SUPABASE_SERVICE_KEY} must be set."
            )

        bridge_user_id = _env_uuid(src, ENV_MULTICA_BRIDGE_USER_ID)
        if bridge_user_id is None:
            raise RuntimeError(
                f"{ENV_MULTICA_BRIDGE_USER_ID} must be set to a valid user UUID; "
                "all multica traffic is enqueued under this single shared user."
            )

        port = _env_int(src, ENV_MULTICA_PORT, _DEFAULT_MULTICA_PORT)
        chat_timeout_s = _env_float(
            src, ENV_MULTICA_CHAT_TIMEOUT, _CHAT_COMPLETIONS_DEFAULT_TIMEOUT
        )
        max_body_bytes = _env_int(src, ENV_MAX_BODY_BYTES, _DEFAULT_MAX_BODY_BYTES)
        shell_default_timeout_s = _env_int(
            src,
            ENV_MULTICA_SHELL_DEFAULT_TIMEOUT,
            _DEFAULT_MULTICA_SHELL_DEFAULT_TIMEOUT,
        )
        shell_max_timeout_s = _env_int(
            src, ENV_MULTICA_SHELL_MAX_TIMEOUT, _DEFAULT_MULTICA_SHELL_MAX_TIMEOUT
        )
        poll_interval_s = _env_float(src, ENV_POLL_INTERVAL, _DEFAULT_POLL_INTERVAL)

        stream_first_chunk_timeout_s = _env_float(
            src,
            ENV_MULTICA_STREAM_FIRST_CHUNK_TIMEOUT,
            _DEFAULT_STREAM_FIRST_CHUNK_TIMEOUT,
        )
        stream_inter_chunk_timeout_s = _env_float(
            src,
            ENV_MULTICA_STREAM_INTER_CHUNK_TIMEOUT,
            _DEFAULT_STREAM_INTER_CHUNK_TIMEOUT,
        )
        stream_poll_interval_s = _env_float(
            src, ENV_MULTICA_STREAM_POLL_INTERVAL, _DEFAULT_STREAM_POLL_INTERVAL
        )

        _require_positive(ENV_MULTICA_PORT, port)
        _require_positive(ENV_MULTICA_CHAT_TIMEOUT, chat_timeout_s)
        _require_positive(ENV_MAX_BODY_BYTES, max_body_bytes)
        _require_positive(ENV_MULTICA_SHELL_DEFAULT_TIMEOUT, shell_default_timeout_s)
        _require_positive(ENV_MULTICA_SHELL_MAX_TIMEOUT, shell_max_timeout_s)
        _require_positive(ENV_POLL_INTERVAL, poll_interval_s)
        _require_positive(
            ENV_MULTICA_STREAM_FIRST_CHUNK_TIMEOUT, stream_first_chunk_timeout_s
        )
        _require_positive(
            ENV_MULTICA_STREAM_INTER_CHUNK_TIMEOUT, stream_inter_chunk_timeout_s
        )
        _require_positive(ENV_MULTICA_STREAM_POLL_INTERVAL, stream_poll_interval_s)
        if shell_default_timeout_s > shell_max_timeout_s:
            raise ValueError(
                f"{ENV_MULTICA_SHELL_DEFAULT_TIMEOUT} ({shell_default_timeout_s}) must "
                f"not exceed {ENV_MULTICA_SHELL_MAX_TIMEOUT} ({shell_max_timeout_s})."
            )

        return cls(
            supabase_url=supabase_url,
            supabase_key=supabase_key,
            bridge_user_id=bridge_user_id,
            llm_api_keys=_split_keys(_get(src, ENV_MULTICA_LLM_API_KEYS)),
            shell_api_keys=_split_keys(_get(src, ENV_MULTICA_SHELL_API_KEYS)),
            bind_host=_get(src, ENV_MULTICA_BIND_HOST) or _DEFAULT_MULTICA_BIND_HOST,
            port=port,
            chat_timeout_s=chat_timeout_s,
            max_body_bytes=max_body_bytes,
            upstream_api_key=_get(src, ENV_MULTICA_UPSTREAM_API_KEY),
            shell_default_timeout_s=shell_default_timeout_s,
            shell_max_timeout_s=shell_max_timeout_s,
            shell_default_cwd=_get(src, ENV_MULTICA_SHELL_DEFAULT_CWD),
            poll_interval_s=poll_interval_s,
            stream_first_chunk_timeout_s=stream_first_chunk_timeout_s,
            stream_inter_chunk_timeout_s=stream_inter_chunk_timeout_s,
            stream_poll_interval_s=stream_poll_interval_s,
        )

    def resolve_shell_timeout(self, requested: int | None) -> int:
        """Clamp a requested per-command timeout into ``[1, shell_max_timeout_s]``.

        ``None`` or a non-positive request falls back to ``shell_default_timeout_s``.
        """
        if requested is None or requested <= 0:
            return self.shell_default_timeout_s
        return min(requested, self.shell_max_timeout_s)
