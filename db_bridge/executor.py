"""Executor worker pool for the DB bridge.

For each channel whose executor runs on this side, a pool of asyncio workers
polls the channel's table, atomically claims a pending row, forwards it to the
real local service over loopback, and writes the response (or error) back.

Forwarding is a faithful raw-body replay: the captured method, path+query, and
(filtered) headers are sent verbatim, preserving pass-through ``Authorization``.
Non-2xx upstream responses (e.g. the gateway's 429 "no capacity") are recorded
as normal completions and propagated to the caller; only transport-level
failures mark a row ``error``.
"""

from __future__ import annotations

import asyncio
import logging
import time

import httpx

from . import crypto, relay
from .channels import Channel, Side, executor_channels
from .config import BridgeConfig
from .db import BridgeDB
from .metrics import get_metrics

logger = logging.getLogger("db_bridge.executor")


def _build_httpx_client() -> httpx.AsyncClient:
    # Per-request timeouts are applied in _forward; this is just the pool.
    return httpx.AsyncClient(
        limits=httpx.Limits(max_connections=200, max_keepalive_connections=50),
        trust_env=False,
        follow_redirects=False,
    )


class Executor:
    """Runs the executor worker pool for one side until stopped."""

    def __init__(
        self,
        db: BridgeDB,
        side: Side,
        *,
        config: BridgeConfig | None = None,
        client: httpx.AsyncClient | None = None,
        worker_prefix: str = "exec",
    ):
        self._db = db
        self._side = side
        self._config = config or db.config
        self._client = client
        self._owns_client = client is None
        self._worker_prefix = worker_prefix
        self._stop = asyncio.Event()
        self._cipher = self._config.build_cipher()
        self._metrics = get_metrics()

    async def connect(self) -> "Executor":
        if self._client is None:
            self._client = _build_httpx_client()
        return self

    async def aclose(self) -> None:
        if self._owns_client and self._client is not None:
            try:
                await self._client.aclose()
            finally:
                self._client = None

    async def __aenter__(self) -> "Executor":
        return await self.connect()

    async def __aexit__(self, *exc: object) -> None:
        await self.aclose()

    # -- forwarding --------------------------------------------------------

    async def _forward(self, channel: Channel, req) -> httpx.Response:
        assert self._client is not None
        url = self._config.upstream_for_group(channel.group) + req.path
        # Decrypt any at-rest-encrypted tokens just before replay.
        headers = relay.filter_request_headers(
            crypto.decrypt_headers(req.headers, self._cipher)
        )
        # For leagent_api channels (executor → leagent backend), inject admin
        # auth so requests from AReaL callers (which lack Supabase JWTs) are
        # accepted by the backend's bridge-aware auth dependency.
        if channel.group == "leagent_api":
            if self._config.admin_api_key:
                headers["x-admin-api-key"] = self._config.admin_api_key
            if self._config.bridge_user_id:
                headers["x-bridge-user-id"] = self._config.bridge_user_id
        return await self._client.request(
            req.method,
            url,
            headers=headers,
            content=req.body,
            timeout=self._config.timeout_for(channel),
        )

    async def process_one(self, channel: Channel, worker_id: str) -> bool:
        """Claim and relay a single request. Returns False if nothing to do."""
        req = await self._db.claim_next(
            channel, worker_id, user_id=self._config.bridge_user_id
        )
        if req is None:
            return False
        started = time.monotonic()
        try:
            resp = await self._forward(channel, req)
        except Exception as exc:  # noqa: BLE001 -- record any transport failure
            logger.warning(
                "executor forward failed channel=%s id=%s: %s",
                channel.name,
                req.id,
                exc,
            )
            self._metrics.record_forward(
                channel.name, ok=False, latency_s=time.monotonic() - started
            )
            completed = await self._db.fail(
                channel,
                req.id,
                worker_id=req.worker_id,
                error=f"{type(exc).__name__}: {exc}",
            )
            if not completed:
                logger.info(
                    "executor skipped stale failure channel=%s id=%s worker=%s",
                    channel.name,
                    req.id,
                    req.worker_id,
                )
            return True

        completed = await self._db.complete(
            channel,
            req.id,
            worker_id=req.worker_id,
            response_status=resp.status_code,
            response_headers=relay.filter_response_headers(resp.headers),
            body=resp.content,
        )
        if not completed:
            logger.info(
                "executor skipped stale completion channel=%s id=%s worker=%s",
                channel.name,
                req.id,
                req.worker_id,
            )
            return True
        self._metrics.record_forward(
            channel.name, ok=True, latency_s=time.monotonic() - started
        )
        # Optional post-completion hardening: scrub the stored token now that the
        # response has been relayed and the row is retained for audit.
        if self._config.redact_tokens_after_complete:
            try:
                await self._db.redact_headers(channel, req.id)
            except Exception as exc:  # noqa: BLE001 -- redaction must never break relay
                logger.warning(
                    "executor token redaction failed channel=%s id=%s: %s",
                    channel.name,
                    req.id,
                    exc,
                )
        logger.info(
            "executor relayed response channel=%s id=%s status=%s bytes=%d",
            channel.name,
            req.id,
            resp.status_code,
            len(resp.content),
        )
        return True

    # -- worker pool -------------------------------------------------------

    async def _worker(self, channel: Channel, worker_id: str) -> None:
        idle_sleep = self._config.poll_interval_s
        while not self._stop.is_set():
            try:
                did_work = await self.process_one(channel, worker_id)
            except asyncio.CancelledError:
                raise
            except Exception:  # noqa: BLE001 -- never let a worker die
                logger.exception("executor worker crashed channel=%s", channel.name)
                did_work = False
            if not did_work:
                await asyncio.sleep(idle_sleep)

    def stop(self) -> None:
        self._stop.set()

    async def _cleanup_loop(self, channels) -> None:
        interval = self._config.cleanup_interval_s
        while not self._stop.is_set():
            try:
                await asyncio.wait_for(self._stop.wait(), timeout=interval)
                return
            except TimeoutError:
                pass
            deleted: dict[str, int] = {}
            for channel in channels:
                try:
                    count = await self._db.cleanup_stale_rows(
                        channel,
                        retention_seconds=self._config.row_retention_seconds,
                        limit=self._config.cleanup_batch_limit,
                    )
                except Exception:  # noqa: BLE001 -- cleanup must never crash the pool
                    logger.exception("executor cleanup failed channel=%s", channel.name)
                    continue
                if count:
                    deleted[channel.name] = count
            if deleted:
                logger.info("executor cleanup side=%s deleted=%s", self._side, deleted)

    async def _stats_loop(self, channels) -> None:
        interval = self._config.stats_interval_s
        while not self._stop.is_set():
            try:
                await asyncio.wait_for(self._stop.wait(), timeout=interval)
                return  # stopped
            except TimeoutError:
                pass
            depth: dict[str, int] = {}
            for channel in channels:
                try:
                    depth[channel.name] = await self._db.count_pending(
                        channel, user_id=self._config.bridge_user_id
                    )
                except Exception:  # noqa: BLE001 -- stats must never crash the pool
                    pass
            logger.info(
                "executor stats side=%s queue_depth=%s metrics=%s",
                self._side,
                depth,
                self._metrics.snapshot(),
            )

    async def run(self) -> None:
        """Spawn the worker pool and run until ``stop()`` is called.

        Note: the stop event is created in ``__init__`` and is never cleared
        here. Clearing it at startup would race with an early ``stop()`` (e.g.
        a caller that stops before this coroutine is first scheduled) and hang
        the pool on ``self._stop.wait()``.
        """
        await self.connect()
        channels = executor_channels(self._side)
        tasks: list[asyncio.Task] = []
        for channel in channels:
            n = self._config.concurrency_for(channel)
            for i in range(n):
                worker_id = f"{self._worker_prefix}-{channel.name}-{i}"
                tasks.append(asyncio.create_task(self._worker(channel, worker_id)))
            logger.debug(
                "executor started channel=%s workers=%d upstream=%s",
                channel.name,
                n,
                self._config.upstream_for_group(channel.group),
            )
        if not tasks:
            logger.warning("executor for side=%s has no channels", self._side)
            return
        worker_count = len(tasks)
        if self._config.stats_interval_s > 0:
            tasks.append(asyncio.create_task(self._stats_loop(channels)))
        if self._config.cleanup_interval_s > 0:
            tasks.append(asyncio.create_task(self._cleanup_loop(channels)))
        logger.info(
            "executor ready side=%s channels=%s workers=%d",
            self._side,
            [channel.name for channel in channels],
            worker_count,
        )
        try:
            await self._stop.wait()
        finally:
            for task in tasks:
                task.cancel()
            await asyncio.gather(*tasks, return_exceptions=True)
            logger.debug("executor stopped side=%s", self._side)
