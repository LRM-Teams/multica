"""Channel model tests: Side/Group literals and side derivation.

Covers the three-sided bridge model introduced for the ``multica_api`` group
(AReaL stub -> multica executor), complementing the per-group channel tests in
``test_gateway_channels.py`` / ``test_leagent_channels.py`` /
``test_env_dispatch_channels.py``.
"""

from __future__ import annotations

from db_bridge.channels import CHANNELS_BY_NAME, Channel, Group, Side


def test_multica_side_and_group_literals_exist():
    assert "multica_api" in Group.__args__  # type: ignore[attr-defined]
    assert "multica" in Side.__args__  # type: ignore[attr-defined]


def test_multica_api_channel_side_derivation():
    # A multica_api channel: the stub runs on the areal host (AReaL calls its
    # local stub) and the executor runs on the multica host (forwards to the
    # real multica Go server over loopback).
    ch = Channel(
        name="probe",
        group="multica_api",
        method="GET",
        path="/probe",
        kind="json",
        default_timeout_s=1.0,
        default_concurrency=1,
    )
    assert ch.stub_side == "areal"
    assert ch.executor_side == "multica"
    # env_dispatch still resolves (it moves to multica_api in a later task;
    # this guard keeps the lookup honest while the registry shape changes).
    assert "env_dispatch" in CHANNELS_BY_NAME
