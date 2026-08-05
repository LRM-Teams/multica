"""Unit tests for diagnostics + guaranteed cleanup (T029) and the
negative-control swap (T027). MulticaClient and run_in_sandbox are mocked —
these tests run anywhere."""

from __future__ import annotations

from typing import Any

import pytest

from e2e_harness import chain
from e2e_harness.chain import run_chain
from e2e_harness.config import HarnessConfig
from e2e_harness.diagnostics import FailureReport, build_failure_report
from e2e_harness.multica_client import DispatchHandle, MulticaAPIError
from e2e_harness.sandbox_exec import ExecResult

ASSEMBLED_DAG = {"segments": [{"closing_event": "e1"}], "edges": []}


def _config(negative_control: bool = False) -> HarnessConfig:
    return HarnessConfig(
        base_url="http://server",
        agent_id="agent-1",
        base_env_id="env-base",
        cube_proxy_url="http://cube-proxy",
        negative_control=negative_control,
    )


def _handle(project: str, env: str, sandbox: str) -> DispatchHandle:
    return DispatchHandle(
        project_id=project,
        env_id=env,
        issue_id=f"issue-{project}",
        agent_run_id=f"run-{project}",
        leader_run_id=f"run-{project}",
        leader_sandbox_id=sandbox,
        rollout_error=None,
        raw={"project_id": project, "rollouts": [{"env_id": env}]},
    )


def _three_handles() -> dict[str, DispatchHandle]:
    return {
        "scratch": _handle("proj-1", "env-1", "sbx-1"),
        "branch": _handle("proj-2", "env-2", "sbx-2"),
        "resume": _handle("proj-3", "env-3", "sbx-3"),
    }


class DiagClient:
    """Mocked client recording deletes; can fail dispatch or cleanup."""

    def __init__(
        self,
        handles: dict[str, DispatchHandle] | None = None,
        dispatch_error: Exception | None = None,
        dag_sequence: list[tuple[int, Any]] | None = None,
        task_runs: Any = None,
        delete_error: Exception | None = None,
    ) -> None:
        self._handles = handles or _three_handles()
        self._dispatch_error = dispatch_error
        self._dag = list(dag_sequence) if dag_sequence else [(200, ASSEMBLED_DAG)]
        self._task_runs = task_runs if task_runs is not None else [{"status": "completed"}]
        self._delete_error = delete_error
        self.deleted: list[str] = []

    def dispatch_scratch(self, issue_spec: dict[str, Any]) -> DispatchHandle:
        if self._dispatch_error is not None:
            raise self._dispatch_error
        return self._handles["scratch"]

    def dispatch_branch(self, env_id: str, mode: str) -> DispatchHandle:
        return self._handles[mode]

    def get_dag(self, project_id: str) -> tuple[int, Any]:
        if len(self._dag) > 1:
            return self._dag.pop(0)
        return self._dag[0]

    def get_task_runs(self, issue_id: str) -> tuple[int, Any]:
        return 200, self._task_runs

    def delete_dispatch(self, project_id: str) -> int:
        if self._delete_error is not None:
            raise self._delete_error
        self.deleted.append(project_id)
        return 204


@pytest.fixture(autouse=True)
def _no_sleep(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(chain.time, "sleep", lambda _sec: None)


def _mock_exec(
    monkeypatch: pytest.MonkeyPatch,
    *,
    lineage_ok: bool = True,
    acceptance_ok: bool = True,
) -> list[str]:
    calls: list[str] = []

    def fake(proxy: str, domain: str, sandbox_id: str, cmd: str, timeout: int) -> ExecResult:
        calls.append(cmd)
        is_lineage = (
            f"test -f {chain.LINEAGE_MARKER_FILE}" in cmd
            or "fixture_calc.calculator import add" in cmd
        )
        if is_lineage:
            return ExecResult(exit_code=0 if lineage_ok else 1, stdout="", stderr="")
        if acceptance_ok:
            return ExecResult(exit_code=0, stdout="4 passed\n", stderr="")
        return ExecResult(
            exit_code=1,
            stdout="FAILED tests/test_calculator.py::test_add_integers - boom\n",
            stderr="",
        )

    monkeypatch.setattr(chain.sandbox_exec, "run_in_sandbox", fake)
    return calls


# --- FailureReport contents per failing phase -------------------------------


def test_report_on_submit_failure() -> None:
    client = DiagClient(
        dispatch_error=MulticaAPIError(
            "env-dispatch failed with status 400", status=400, body={"error": "bad env"}
        )
    )
    chain_run = run_chain(_config(), client)
    report = chain_run.failure
    assert chain_run.outcome == "failed"
    assert report is not None
    assert report.stage == "scratch"
    assert report.phase == "submit"
    assert report.dispatch_response == {"status": 400, "body": {"error": "bad env"}}
    # Nothing was submitted, so nothing to clean up.
    assert client.deleted == []
    assert report.cleanup_results == []


def test_report_on_dag_failure_captures_snapshots() -> None:
    # run_in_sandbox is never reached: the DAG fails before verification.
    client = DiagClient(
        dag_sequence=[(202, {"status": "in_progress"}), (200, {"status": "failed"})],
        task_runs=[{"status": "failed", "error": "agent crashed"}],
    )
    chain_run = run_chain(_config(), client)
    report = chain_run.failure
    assert report is not None
    assert report.phase == "execution"
    assert report.dag_snapshot == {"status": 200, "body": {"status": "failed"}}
    assert report.task_snapshot is not None and "agent crashed" in report.task_snapshot
    assert "WORKED BUT FAILED" in report.render()


def test_report_on_verification_failure(monkeypatch: pytest.MonkeyPatch) -> None:
    _mock_exec(monkeypatch, acceptance_ok=False)
    client = DiagClient()
    chain_run = run_chain(_config(), client)
    report = chain_run.failure
    assert report is not None
    assert report.stage == "scratch"
    assert report.phase == "verification"
    assert "test_add_integers" in report.summary
    # Dispatch response is the raw 201 body captured from the handle.
    assert report.dispatch_response == {"project_id": "proj-1", "rollouts": [{"env_id": "env-1"}]}


# --- Guaranteed cleanup (T026) ----------------------------------------------


def test_cleanup_in_finally_on_lineage_stage_failure(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    _mock_exec(monkeypatch, lineage_ok=False)
    client = DiagClient()
    chain_run = run_chain(_config(), client)
    # Both submitted stages are cleaned even though the chain stopped at
    # branch.
    assert client.deleted == ["proj-1", "proj-2"]
    report = chain_run.failure
    assert report is not None
    assert report.stage == "branch"
    assert any("scratch/proj-1: DELETE 204" in e for e in report.cleanup_results)
    assert any("branch/proj-2: DELETE 204" in e for e in report.cleanup_results)


def test_cleanup_error_does_not_mask_original_failure(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    _mock_exec(monkeypatch, acceptance_ok=False)
    client = DiagClient(
        delete_error=MulticaAPIError("delete transport error: connection reset")
    )
    chain_run = run_chain(_config(), client)
    # The original failure stands; the cleanup error is recorded, not raised.
    assert chain_run.outcome == "failed"
    report = chain_run.failure
    assert report is not None
    assert report.phase == "verification"
    assert "test_add_integers" in report.summary
    assert any("cleanup error" in e for e in report.cleanup_results)
    assert "cleanup error" in report.render()


def test_passing_chain_has_no_failure_report(monkeypatch: pytest.MonkeyPatch) -> None:
    _mock_exec(monkeypatch)
    client = DiagClient()
    chain_run = run_chain(_config(), client)
    assert chain_run.outcome == "passed"
    assert chain_run.failure is None
    # Cleanup still ran for all three submitted stages.
    assert client.deleted == ["proj-1", "proj-2", "proj-3"]


# --- render() triage block ---------------------------------------------------


def test_render_contains_all_sections_and_triage_hints() -> None:
    report = FailureReport(
        stage="branch",
        phase="verification",
        summary="lineage check failed at stage branch: missing prior-stage changes",
        dispatch_response={"project_id": "p"},
        dag_snapshot={"status": 202, "body": {"status": "in_progress"}},
        task_snapshot="no task runs (issue never picked up)",
        cleanup_results=["branch/proj-2: DELETE 204"],
    )
    rendered = report.render()
    for expected in (
        "E2E FAILURE TRIAGE",
        "stage            : branch",
        "phase            : verification",
        "dispatch_response",
        "dag_snapshot",
        "task_snapshot",
        "cleanup_results",
        "NEVER PICKED UP",
        "FORK REGRESSION",
    ):
        assert expected in rendered


def test_build_failure_report_returns_none_when_all_passed() -> None:
    stage = chain.StageResult(stage="scratch", mode_sent="scratch", dispatch=None)
    stage.dag_status = chain.DAG_ASSEMBLED
    stage.segments = 1
    stage.acceptance_ok = True
    chain_run = chain.ChainRun(run_id="r", stages=[stage])
    assert build_failure_report(chain_run, []) is None


# --- Negative control (T027) -------------------------------------------------


def test_negative_control_swaps_acceptance_and_fails_at_verification(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    calls = _mock_exec(monkeypatch, acceptance_ok=True)
    # Even a "successful" exec transport must fail: the negative-control
    # command is unsatisfiable, so fake exec returning exit 1 for it.
    def fake(proxy: str, domain: str, sandbox_id: str, cmd: str, timeout: int) -> ExecResult:
        calls.append(cmd)
        return ExecResult(exit_code=1, stdout="", stderr="")

    monkeypatch.setattr(chain.sandbox_exec, "run_in_sandbox", fake)
    client = DiagClient()
    chain_run = run_chain(_config(negative_control=True), client)

    assert chain_run.negative_control is True
    assert chain_run.outcome == "failed"
    report = chain_run.failure
    assert report is not None
    assert report.stage == "scratch"
    assert report.phase == "verification"
    assert "negative-control" in report.summary
    # The swapped command, not the pytest selection, was executed.
    assert len(calls) == 1
    assert chain.NEGATIVE_CONTROL_CHECK in calls[0]
    assert "pytest" not in calls[0]
