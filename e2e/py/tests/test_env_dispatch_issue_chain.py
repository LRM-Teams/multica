"""THE e2e: chained env-dispatch issue test (US1 scratch + US2 verify + US3 chain).

Skips unless the shared deployment is configured (contracts/harness-config.md
"Required" table). Run explicitly: `pytest -m e2e -v`.
"""

from __future__ import annotations

import uuid
from pathlib import Path

import pytest

from conftest import requires_env
from e2e_harness.auth import resolve_api_key
from e2e_harness.chain import ChainRun, build_issue_spec, run_chain, run_scratch_stage
from e2e_harness.multica_client import MulticaClient

pytestmark = [pytest.mark.e2e, requires_env]


def _client(harness_config) -> MulticaClient:
    credentials_path = (
        Path(harness_config.credentials_file) if harness_config.credentials_file else None
    )
    api_key = resolve_api_key(harness_config.api_key, credentials_path=credentials_path)
    return MulticaClient.from_config(harness_config, api_key)


def test_scratch_stage_completes_and_passes_acceptance(harness_config) -> None:
    """US1+US2: scratch dispatch -> DAG assembled (>=1 segment) -> task
    terminal -> harness-run acceptance checks exit 0 in the leader sandbox."""
    client = _client(harness_config)
    issue_spec = build_issue_spec(run_id=uuid.uuid4().hex[:12])
    result = run_scratch_stage(harness_config, client, issue_spec)
    try:
        # US1: dispatch submitted, DAG assembled with >= 1 segment, terminal
        # detail captured from task-runs.
        assert result.dispatch is not None, result.error
        assert result.dispatch.project_id, result.error
        assert result.dag_status == "assembled", (
            f"stage={result.stage} phase={result.phase} "
            f"dag_status={result.dag_status} error={result.error}"
        )
        assert result.segments >= 1, result.error
        assert result.task_status, "task-runs terminal detail missing"

        # US2 (T019): completion proven by harness-executed checks, not the
        # agent's self-report; checks ran against this stage's leader sandbox.
        assert result.acceptance_ok is True, (
            f"acceptance failed in phase={result.phase}: {result.error} "
            f"failed_checks={result.failed_checks}"
        )
        assert result.acceptance_sandbox_id is not None
        assert result.acceptance_sandbox_id == result.dispatch.leader_sandbox_id
        assert result.passed, result.error
    finally:
        # Guaranteed cleanup: DELETE is idempotent (204 even if already gone).
        if result.dispatch is not None and result.dispatch.project_id:
            client.delete_dispatch(result.dispatch.project_id)


def _stage_summary(chain_run: ChainRun) -> str:
    return "; ".join(
        f"{s.stage}(phase={s.phase}, dag={s.dag_status}, "
        f"lineage={s.lineage_ok}, acceptance={s.acceptance_ok}, error={s.error})"
        for s in chain_run.stages
    )


def test_full_chain_branch_resume_lineage(harness_config) -> None:
    """US3: scratch -> branch -> resume; lineage markers present at both
    lineage stages and acceptance checks green at every stage (US3 scenarios
    1-2, SC-006). Cleanup of every submitted stage is guaranteed inside
    run_chain (T026)."""
    client = _client(harness_config)
    chain_run = run_chain(harness_config, client)
    report = chain_run.failure.render() if chain_run.failure else "(no failure)"
    summary = _stage_summary(chain_run)
    assert len(chain_run.stages) == 3, f"chain stopped early: {summary}\n{report}"
    scratch, branch, resume = chain_run.stages

    assert scratch.passed, f"scratch failed: {scratch.error}\n{report}"
    # env_id threading: each stage forked the previous stage's env.
    assert branch.dispatch is not None and scratch.dispatch is not None
    assert resume.dispatch is not None and branch.dispatch is not None
    env_ids = [
        scratch.dispatch.env_id,
        branch.dispatch.env_id,
        resume.dispatch.env_id,
    ]
    assert len(set(env_ids)) == 3, f"stage envs are not distinct: {env_ids}"

    # Lineage asserted BEFORE crediting the agent, at both lineage stages.
    assert branch.lineage_ok is True, (
        f"branch lineage markers missing: {branch.failed_lineage_markers} "
        f"error={branch.error}\n{report}"
    )
    assert resume.lineage_ok is True, (
        f"resume lineage markers missing: {resume.failed_lineage_markers} "
        f"error={resume.error}\n{report}"
    )

    # Acceptance checks green at every stage.
    for stage in chain_run.stages:
        assert stage.dag_status == "assembled" and stage.segments >= 1, (
            f"{stage.stage}: {stage.error}\n{report}"
        )
        assert stage.acceptance_ok is True, (
            f"{stage.stage} acceptance failed: {stage.error} "
            f"failed_checks={stage.failed_checks}\n{report}"
        )
    assert chain_run.outcome == "passed", f"{summary}\n{report}"
