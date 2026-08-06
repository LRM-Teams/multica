"""Negative-control e2e (US4, FR-010, SC-003).

With E2E_NEGATIVE_CONTROL=1 the acceptance command is swapped for an
unsatisfiable check, so the run MUST fail at stage `scratch` / phase
`verification` — proving the suite is not vacuously green. Run explicitly:

    E2E_NEGATIVE_CONTROL=1 pytest -m e2e -v tests/test_negative_control.py
"""

from __future__ import annotations

from pathlib import Path

import pytest

from conftest import requires_env
from e2e_harness.auth import resolve_api_key
from e2e_harness.chain import run_chain
from e2e_harness.multica_client import MulticaClient

pytestmark = [pytest.mark.e2e, requires_env]


def test_negative_control_fails_at_scratch_verification(harness_config) -> None:
    if not harness_config.negative_control:
        pytest.skip("negative-control mode requires E2E_NEGATIVE_CONTROL=1")
    credentials_path = (
        Path(harness_config.credentials_file) if harness_config.credentials_file else None
    )
    api_key = resolve_api_key(harness_config.api_key, credentials_path=credentials_path)
    client = MulticaClient.from_config(harness_config, api_key)

    chain_run = run_chain(harness_config, client)

    # The run must fail — a green negative control means the suite is vacuous.
    assert chain_run.outcome == "failed", (
        "negative control unexpectedly PASSED — the suite is vacuously green"
    )
    assert chain_run.negative_control is True

    # The failure is localized to stage scratch / phase verification, and the
    # rendered report identifies that phase (SC-003).
    report = chain_run.failure
    assert report is not None, "failure report missing on a failed run"
    assert report.stage == "scratch"
    assert report.phase == "verification"
    rendered = report.render()
    assert "verification" in rendered
    assert "scratch" in rendered

    # Guaranteed cleanup ran for the submitted stage (SC-005).
    assert report.cleanup_results, "no cleanup results recorded"
    assert any("DELETE 204" in entry for entry in report.cleanup_results)
