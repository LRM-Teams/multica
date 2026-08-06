"""Lightweight in-process metrics for the DB bridge.

Counters and timings are per-channel and per-process (the stub and executor run
as separate processes, so each tracks the half it sees). ``snapshot()`` renders
a compact dict suitable for periodic structured logging.
"""

from __future__ import annotations

import threading
from dataclasses import dataclass


@dataclass
class ChannelStats:
    # Stub side
    enqueued: int = 0
    inflight: int = 0
    done: int = 0
    errors: int = 0
    timeouts: int = 0
    request_bytes: int = 0
    response_bytes: int = 0
    max_request_bytes: int = 0
    max_response_bytes: int = 0
    e2e_latency_sum: float = 0.0
    e2e_count: int = 0
    # Executor side
    forwarded: int = 0
    forward_errors: int = 0
    forward_latency_sum: float = 0.0

    def as_dict(self) -> dict[str, float | int]:
        d: dict[str, float | int] = {
            "enqueued": self.enqueued,
            "inflight": self.inflight,
            "done": self.done,
            "errors": self.errors,
            "timeouts": self.timeouts,
            "forwarded": self.forwarded,
            "forward_errors": self.forward_errors,
            "max_request_bytes": self.max_request_bytes,
            "max_response_bytes": self.max_response_bytes,
        }
        if self.e2e_count:
            d["avg_e2e_ms"] = round(1000 * self.e2e_latency_sum / self.e2e_count, 1)
        if self.forwarded:
            d["avg_forward_ms"] = round(
                1000 * self.forward_latency_sum / self.forwarded, 1
            )
        return d


class BridgeMetrics:
    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._channels: dict[str, ChannelStats] = {}

    def _stats(self, channel: str) -> ChannelStats:
        s = self._channels.get(channel)
        if s is None:
            s = ChannelStats()
            self._channels[channel] = s
        return s

    # -- stub side ---------------------------------------------------------

    def record_enqueue(self, channel: str, request_bytes: int) -> None:
        with self._lock:
            s = self._stats(channel)
            s.enqueued += 1
            s.inflight += 1
            s.request_bytes += request_bytes
            s.max_request_bytes = max(s.max_request_bytes, request_bytes)

    def record_result(
        self, channel: str, outcome: str, *, response_bytes: int = 0, latency_s: float = 0.0
    ) -> None:
        with self._lock:
            s = self._stats(channel)
            s.inflight = max(0, s.inflight - 1)
            if outcome == "done":
                s.done += 1
                s.response_bytes += response_bytes
                s.max_response_bytes = max(s.max_response_bytes, response_bytes)
                s.e2e_latency_sum += latency_s
                s.e2e_count += 1
            elif outcome == "timeout":
                s.timeouts += 1
            else:
                s.errors += 1

    # -- executor side -----------------------------------------------------

    def record_forward(self, channel: str, *, ok: bool, latency_s: float) -> None:
        with self._lock:
            s = self._stats(channel)
            if ok:
                s.forwarded += 1
                s.forward_latency_sum += latency_s
            else:
                s.forward_errors += 1

    # -- reporting ---------------------------------------------------------

    def snapshot(self) -> dict[str, dict[str, float | int]]:
        with self._lock:
            return {name: s.as_dict() for name, s in self._channels.items()}


_METRICS = BridgeMetrics()


def get_metrics() -> BridgeMetrics:
    return _METRICS
