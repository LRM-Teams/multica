"""Channel registry for the DB bridge.

A *channel* is one bridged HTTP endpoint. Each channel maps a request path to a
dedicated Supabase table and records which side hosts the stub (the caller's
side) versus the executor (the callee's side, where the real service runs).

Three groups:

* ``gateway``      -- le-agent calls the AReaL proxy gateway. Stub runs on the
                      le-agent host; executor runs on the AReaL host and
                      forwards to the real gateway.
* ``leagent_api``  -- AReaL calls the le-agent API. Stub runs on the AReaL
                      host; executor runs on the le-agent host and forwards to
                      the real le-agent API.
* ``multica_api``  -- AReaL calls the multica API (env-dispatch / DAG fetch).
                      Stub runs on the AReaL host; executor runs on the multica
                      host and forwards to the real multica Go server.

Per side:

* ``multica`` side -- stub serves ``gateway`` channels; executor runs
                      ``multica_api`` channels.
* ``areal`` side   -- stub serves ``leagent_api`` + ``multica_api`` channels;
                      executor runs ``gateway`` channels.
* ``leagent`` side -- executor runs ``leagent_api`` channels (hosts no stub).
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Final, Literal

Group = Literal["gateway", "leagent_api", "multica_api"]
Side = Literal["leagent", "areal", "multica"]
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
        return "multica" if self.group == "gateway" else "areal"

    @property
    def executor_side(self) -> Side:
        """Host whose executor worker forwards this channel to the real service."""
        if self.group == "gateway":
            return "areal"
        if self.group == "multica_api":
            return "multica"
        return "leagent"


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
    Channel(
        name="rl_close_segment",
        group="gateway",
        method="POST",
        path="/rl/close_segment",
        kind="json",
        default_timeout_s=30.0,
        default_concurrency=4,
    ),
    Channel(
        name="export_trajectories",
        group="gateway",
        method="POST",
        path="/export_trajectories",
        kind="json",
        # Paired with rl_close_segment: multica exports the just-closed segment
        # to read its tensor refs, and a segment it cannot export is a segment
        # it cannot record. The export returns remotized refs rather than tensor
        # bytes so the payload stays small, but it waits on the data proxy
        # serialising a whole trajectory -- hence the wider timeout.
        default_timeout_s=120.0,
        default_concurrency=4,
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
    # -- multica_api group (AReaL -> multica API) --------------------------
    Channel(
        name="env_dispatch",
        group="multica_api",
        method="POST",
        path="/api/v1/env-dispatch",
        kind="json",
        default_timeout_s=600.0,
        default_concurrency=8,
    ),
    Channel(
        name="env_dispatch_delete",
        group="multica_api",
        method="DELETE",
        path="/api/v1/env-dispatch/{projectID}",
        kind="json",
        default_timeout_s=60.0,
        default_concurrency=4,
    ),
    Channel(
        name="env_dispatch_dag",
        group="multica_api",
        method="GET",
        path="/api/v1/env-dispatch/{projectID}/dag",
        kind="json",
        default_timeout_s=30.0,
        default_concurrency=4,
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
