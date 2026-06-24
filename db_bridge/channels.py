"""Channel registry for the DB bridge.

A *channel* is one bridged HTTP endpoint. Each channel maps a request path to a
dedicated Supabase table and records which side hosts the stub (the caller's
side) versus the executor (the callee's side, where the real service runs).

Two groups:

* ``gateway``      -- le-agent calls the AReaL proxy gateway. Stub runs on the
                      le-agent host; executor runs on the AReaL host and
                      forwards to the real gateway.
* ``leagent_api``  -- AReaL calls the le-agent API. Stub runs on the AReaL
                      host; executor runs on the le-agent host and forwards to
                      the real le-agent API.

Per host:

* ``leagent`` side -- stub serves ``gateway`` channels; executor runs
                      ``leagent_api`` channels.
* ``areal`` side   -- stub serves ``leagent_api`` channels; executor runs
                      ``gateway`` channels.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Final, Literal

Group = Literal["gateway", "leagent_api"]
Side = Literal["leagent", "areal"]
Kind = Literal["json", "multipart"]


@dataclass(frozen=True, slots=True)
class Channel:
    """A single bridged endpoint."""

    name: str
    """Stable identifier, also the suffix of the table name."""

    group: Group
    """Which traffic group this channel belongs to."""

    method: str
    """HTTP method the stub serves and the executor replays."""

    path: str
    """Request path the stub serves and the executor forwards (no host)."""

    kind: Kind
    """Body shape, used for audit metadata extraction and the size guard."""

    default_timeout_s: float
    """How long the stub waits for a response before returning 504."""

    default_concurrency: int
    """Number of executor worker coroutines for this channel's table."""

    @property
    def table(self) -> str:
        """Supabase table backing this channel."""
        return f"rpc_{self.name}"

    @property
    def stub_side(self) -> Side:
        """Host whose stub server serves this channel."""
        return "leagent" if self.group == "gateway" else "areal"

    @property
    def executor_side(self) -> Side:
        """Host whose executor worker forwards this channel to the real service."""
        return "areal" if self.group == "gateway" else "leagent"


# ---------------------------------------------------------------------------
# Registry
# ---------------------------------------------------------------------------

CHANNELS: Final[tuple[Channel, ...]] = (
    # -- gateway group (le-agent -> AReaL proxy gateway) --------------------
    Channel(
        name="rl_start_session",
        group="gateway",
        method="POST",
        path="/rl/start_session",
        kind="json",
        default_timeout_s=30.0,
        default_concurrency=4,
    ),
    Channel(
        name="rl_set_reward",
        group="gateway",
        method="POST",
        path="/rl/set_reward",
        kind="json",
        default_timeout_s=30.0,
        default_concurrency=4,
    ),
    Channel(
        name="rl_end_session",
        group="gateway",
        method="POST",
        path="/rl/end_session",
        kind="json",
        default_timeout_s=30.0,
        default_concurrency=4,
    ),
    Channel(
        name="chat_completions",
        group="gateway",
        method="POST",
        path="/chat/completions",
        kind="json",
        default_timeout_s=180.0,
        default_concurrency=32,
    ),
    # -- leagent_api group (AReaL -> le-agent API) -------------------------
    Channel(
        name="agent_start",
        group="leagent_api",
        method="POST",
        path="/api/agent/start",
        kind="multipart",
        default_timeout_s=300.0,
        default_concurrency=8,
    ),
    Channel(
        name="agent_start_branch",
        group="leagent_api",
        method="POST",
        path="/api/agent/start-branch",
        kind="json",
        default_timeout_s=300.0,
        default_concurrency=8,
    ),
)

CHANNELS_BY_NAME: Final[dict[str, Channel]] = {c.name: c for c in CHANNELS}
CHANNELS_BY_PATH: Final[dict[str, Channel]] = {c.path: c for c in CHANNELS}
TABLE_NAMES: Final[tuple[str, ...]] = tuple(c.table for c in CHANNELS)


def channels_for_group(group: Group) -> tuple[Channel, ...]:
    return tuple(c for c in CHANNELS if c.group == group)


def stub_channels(side: Side) -> tuple[Channel, ...]:
    """Channels whose stub server runs on *side*."""
    return tuple(c for c in CHANNELS if c.stub_side == side)


def executor_channels(side: Side) -> tuple[Channel, ...]:
    """Channels whose executor worker runs on *side*."""
    return tuple(c for c in CHANNELS if c.executor_side == side)
