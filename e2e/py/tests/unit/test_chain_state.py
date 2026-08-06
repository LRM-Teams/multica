"""Unit tests for e2e_harness.chain stage transitions (T016) and the
acceptance verification phase (T020). MulticaClient and run_in_sandbox are
mocked — these tests run anywhere."""

from __future__ import annotations

import itertools
from typing import Any

import pytest

from e2e_harness import chain
from e2e_harness.chain import (
    DAG_ASSEMBLED,
    DAG_FAILED,
    DAG_IN_PROGRESS,
    DAG_TIMEOUT,
    PHASE_EXECUTION,
    PHASE_SUBMIT,
    PHASE_VERIFICATION,
    StageResult,
    build_issue_spec,
    run_scratch_stage,
)
from e2e_harness.config import HarnessConfig
from e2e_harness.multica_client import DispatchHandle, MulticaAPIError
from e2e_harness.sandbox_exec import ExecResult

ASSEMBLED_DAG = {"segments": [{"closing_event": "e1"}], "edges": [], "score_max": 1}


def _config() -> HarnessConfig:
    return HarnessConfig(
        base_url="http://server",
        agent_id="agent-1",
        base_env_id="env-base",
        cube_proxy_url="http://cube-proxy",
    )


def _handle(
    error: str | None = None,
    agent_run_id: str | None = "run-1",
    sandbox: str | None = "sbx-leader",
) -> DispatchHandle:
    return DispatchHandle(
        project_id="proj-1",
        env_id="env-new",
        issue_id="issue-1",
        agent_run_id=agent_run_id,
        leader_run_id="run-1",
        leader_sandbox_id=sandbox,
        rollout_error=error,
        raw={},
    )


class FakeClient:
    """Mocked MulticaClient subset (chain.StageClient protocol)."""

    def __init__(
        self,
        handle: DispatchHandle | None = None,
        dispatch_error: Exception | None = None,
        dag_sequence: list[tuple[int, Any]] | None = None,
        task_runs_sequence: list[Any] | None = None,
    ) -> None:
        self._handle = handle
        self._dispatch_error = dispatch_error
        self._dag = list(dag_sequence or [(200, ASSEMBLED_DAG)])
        self._runs = list(task_runs_sequence or [])
        self.dag_calls = 0
        self.submitted_issue: dict[str, Any] | None = None

    def dispatch_scratch(self, issue_spec: dict[str, Any]) -> DispatchHandle:
        self.submitted_issue = issue_spec
        if self._dispatch_error is not None:
            raise self._dispatch_error
        assert self._handle is not None
        return self._handle

    def get_dag(self, project_id: str) -> tuple[int, Any]:
        self.dag_calls += 1
        if len(self._dag) > 1:
            return self._dag.pop(0)
        return self._dag[0]

    def get_task_runs(self, issue_id: str) -> tuple[int, Any]:
        if len(self._runs) > 1:
            return 200, self._runs.pop(0)
        return 200, (self._runs[0] if self._runs else [])


@pytest.fixture(autouse=True)
def _no_sleep(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(chain.time, "sleep", lambda _sec: None)


@pytest.fixture
def _acceptance_ok(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(
        chain.sandbox_exec,
        "run_in_sandbox",
        lambda *args, **kwargs: ExecResult(
            exit_code=0, stdout="4 passed\n", stderr="", transport_error=None
        ),
    )


# --- T015: IssueSpec construction -----------------------------------------


def test_issue_spec_has_run_id_suffix_and_fixture_checks() -> None:
    spec = build_issue_spec(run_id="abc123")
    assert "abc123" in spec["title"]
    assert spec["fail_to_pass"] == chain.FIXTURE_FAIL_TO_PASS
    assert spec["pass_to_pass"] == chain.FIXTURE_PASS_TO_PASS
    for node in spec["fail_to_pass"]:
        assert node.startswith("tests/test_calculator.py::")
    # End-state criteria (FR-017): behavior + lineage marker file.
    assert any("test_calculator" in c for c in spec["acceptance_criteria"])
    assert any(chain.LINEAGE_MARKER_FILE in c for c in spec["acceptance_criteria"])


def test_issue_spec_run_id_generated_when_omitted() -> None:
    first = build_issue_spec()
    second = build_issue_spec()
    assert first["title"] != second["title"]


# --- T016: stage state machine ---------------------------------------------


@pytest.mark.usefixtures("_acceptance_ok")
def test_wait_loop_phase_transitions_until_assembled() -> None:
    client = FakeClient(
        handle=_handle(),
        dag_sequence=[(202, {"status": "in_progress"})] * 3 + [(200, ASSEMBLED_DAG)],
        task_runs_sequence=[
            [],
            [{"status": "pending"}],
            [{"status": "running"}],
            [{"status": "completed"}],
        ],
    )
    result = run_scratch_stage(_config(), client, build_issue_spec("r1"))
    assert client.dag_calls == 4
    assert result.dag_status == DAG_ASSEMBLED
    assert result.segments == 1
    assert result.phase == PHASE_VERIFICATION
    assert result.task_status is not None and "status=completed" in result.task_status
    assert result.passed


@pytest.mark.usefixtures("_acceptance_ok")
def test_dag_failed_status_is_terminal_failure_worked_but_failed() -> None:
    client = FakeClient(
        handle=_handle(),
        dag_sequence=[(202, {"status": "in_progress"}), (200, {"status": "failed"})],
        task_runs_sequence=[[{"status": "failed", "error": "agent crashed"}]],
    )
    result = run_scratch_stage(_config(), client, build_issue_spec("r2"))
    assert result.dag_status == DAG_FAILED
    assert not result.passed
    assert result.error is not None
    assert "DAG assembly failed" in result.error
    # Worked-but-failed detail from task-runs.
    assert "agent crashed" in result.error


@pytest.mark.usefixtures("_acceptance_ok")
def test_dag_failed_with_no_tasks_means_never_picked_up() -> None:
    client = FakeClient(
        handle=_handle(),
        dag_sequence=[(200, {"status": "failed"})],
        task_runs_sequence=[[]],
    )
    result = run_scratch_stage(_config(), client, build_issue_spec("r3"))
    assert result.dag_status == DAG_FAILED
    assert result.task_status is not None
    assert "never picked up" in result.task_status


def test_wait_timeout_yields_timeout_outcome(monkeypatch: pytest.MonkeyPatch) -> None:
    clock = itertools.chain([0.0, 0.0, 0.0], itertools.cycle([2000.0]))
    monkeypatch.setattr(chain.time, "monotonic", lambda: next(clock))
    client = FakeClient(
        handle=_handle(),
        dag_sequence=[(202, {"status": "in_progress"})],
        task_runs_sequence=[[{"status": "running"}]],
    )
    result = run_scratch_stage(_config(), client, build_issue_spec("r4"))
    assert result.dag_status == DAG_TIMEOUT
    assert not result.passed
    assert result.error is not None and "stage timeout" in result.error
    assert result.phase == PHASE_EXECUTION


def test_per_rollout_error_fails_at_submit() -> None:
    handle = _handle(error="sandbox fork failed", agent_run_id=None)
    client = FakeClient(handle=handle)
    result = run_scratch_stage(_config(), client, build_issue_spec("r5"))
    assert result.phase == PHASE_SUBMIT
    assert result.dag_status == DAG_IN_PROGRESS
    assert not result.passed
    assert result.error is not None
    assert "submit failed" in result.error
    assert "sandbox fork failed" in result.error
    # Handle is preserved so the caller can still clean up the project.
    assert result.dispatch is not None and result.dispatch.project_id == "proj-1"


def test_dispatch_http_error_fails_at_submit() -> None:
    client = FakeClient(
        dispatch_error=MulticaAPIError(
            "env-dispatch failed with status 400", status=400, body={"error": "bad"}
        )
    )
    result = run_scratch_stage(_config(), client, build_issue_spec("r6"))
    assert result.phase == PHASE_SUBMIT
    assert result.dispatch is None
    assert not result.passed
    assert result.error is not None and "400" in result.error


@pytest.mark.usefixtures("_acceptance_ok")
def test_assembled_dag_with_zero_segments_fails_verification() -> None:
    client = FakeClient(
        handle=_handle(),
        dag_sequence=[(200, {"segments": [], "edges": []})],
        task_runs_sequence=[[{"status": "completed"}]],
    )
    result = run_scratch_stage(_config(), client, build_issue_spec("r7"))
    assert result.dag_status == DAG_ASSEMBLED
    assert result.segments == 0
    assert result.phase == PHASE_VERIFICATION
    assert not result.passed
    assert result.error is not None and "0 segments" in result.error


# --- T020: acceptance verification (mocked run_in_sandbox) ------------------


def test_acceptance_exit_zero_passes_and_uses_leader_sandbox(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    calls: list[tuple[tuple[Any, ...], dict[str, Any]]] = []

    def fake_exec(*args: Any, **kwargs: Any) -> ExecResult:
        calls.append((args, kwargs))
        return ExecResult(exit_code=0, stdout="4 passed\n", stderr="")

    monkeypatch.setattr(chain.sandbox_exec, "run_in_sandbox", fake_exec)
    spec = build_issue_spec("r8")
    client = FakeClient(handle=_handle(sandbox="sbx-leader"))
    result = run_scratch_stage(_config(), client, spec)
    assert result.acceptance_ok is True
    assert result.passed
    assert result.acceptance_sandbox_id == "sbx-leader"
    assert len(calls) == 1
    args, _ = calls[0]
    # (cube_proxy_url, cube_domain, sandbox_id, bash_cmd, timeout_sec)
    assert args[2] == "sbx-leader"
    cmd = args[3]
    assert f"cd {chain.FIXTURE_REPO_PATH}" in cmd
    for node in spec["fail_to_pass"] + spec["pass_to_pass"]:
        assert node in cmd


def test_acceptance_nonzero_exit_lists_failed_checks(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    stdout = (
        "tests/test_calculator.py F.F\n"
        "FAILED tests/test_calculator.py::test_add_integers - NotImplementedError\n"
        "FAILED tests/test_calculator.py::test_add_floats - NotImplementedError\n"
    )
    monkeypatch.setattr(
        chain.sandbox_exec,
        "run_in_sandbox",
        lambda *a, **k: ExecResult(exit_code=1, stdout=stdout, stderr=""),
    )
    client = FakeClient(handle=_handle())
    result = run_scratch_stage(_config(), client, build_issue_spec("r9"))
    assert result.acceptance_ok is False
    assert result.phase == PHASE_VERIFICATION
    assert not result.passed
    assert result.failed_checks == [
        "tests/test_calculator.py::test_add_integers",
        "tests/test_calculator.py::test_add_floats",
    ]
    assert result.error is not None
    assert "test_add_integers" in result.error


def test_acceptance_transport_error_is_verification_failure(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    monkeypatch.setattr(
        chain.sandbox_exec,
        "run_in_sandbox",
        lambda *a, **k: ExecResult(
            exit_code=None,
            stdout="",
            stderr="",
            transport_error="missing __EXIT_CODE__ sentinel in /execute stream",
        ),
    )
    client = FakeClient(handle=_handle())
    result = run_scratch_stage(_config(), client, build_issue_spec("r10"))
    # Transport failure must surface as a verification failure, never skipped.
    assert result.acceptance_ok is False
    assert not result.passed
    assert result.error is not None and "transport error" in result.error


def test_missing_leader_sandbox_fails_verification() -> None:
    client = FakeClient(handle=_handle(sandbox=None))
    result = run_scratch_stage(_config(), client, build_issue_spec("r11"))
    assert result.phase == PHASE_VERIFICATION
    assert result.acceptance_ok is False
    assert not result.passed
    assert result.error is not None and "leader sandbox" in result.error


@pytest.mark.usefixtures("_acceptance_ok")
def test_stage_result_duration_recorded() -> None:
    client = FakeClient(handle=_handle())
    result = run_scratch_stage(_config(), client, build_issue_spec("r12"))
    assert isinstance(result, StageResult)
    assert result.duration_sec >= 0.0


# --- T024: chain orchestration (scratch -> branch -> resume) ----------------

from e2e_harness.chain import ChainRun, run_chain


def _stage_handle(project: str, env: str, issue: str, sandbox: str) -> DispatchHandle:
    return DispatchHandle(
        project_id=project,
        env_id=env,
        issue_id=issue,
        agent_run_id=f"run-{project}",
        leader_run_id=f"run-{project}",
        leader_sandbox_id=sandbox,
        rollout_error=None,
        raw={},
    )


class ChainFakeClient:
    """Mocked client for chain tests: per-project DAG sequences, records
    branch dispatches."""

    def __init__(
        self,
        handles: dict[str, DispatchHandle],
        dag_by_project: dict[str, list[tuple[int, Any]]] | None = None,
        task_runs: Any = None,
    ) -> None:
        # handles keyed by stage name: scratch / branch / resume
        self._handles = handles
        self._dag = {k: list(v) for k, v in (dag_by_project or {}).items()}
        self._task_runs = task_runs if task_runs is not None else [{"status": "completed"}]
        self.scratch_calls = 0
        self.branch_calls: list[tuple[str, str]] = []  # (env_id, mode)
        self.dag_calls_by_project: dict[str, int] = {}
        self.deleted: list[str] = []

    def dispatch_scratch(self, issue_spec: dict[str, Any]) -> DispatchHandle:
        self.scratch_calls += 1
        return self._handles["scratch"]

    def dispatch_branch(self, env_id: str, mode: str) -> DispatchHandle:
        self.branch_calls.append((env_id, mode))
        return self._handles[mode]

    def get_dag(self, project_id: str) -> tuple[int, Any]:
        self.dag_calls_by_project[project_id] = (
            self.dag_calls_by_project.get(project_id, 0) + 1
        )
        seq = self._dag.get(project_id)
        if not seq:
            return 200, ASSEMBLED_DAG
        if len(seq) > 1:
            return seq.pop(0)
        return seq[0]

    def get_task_runs(self, issue_id: str) -> tuple[int, Any]:
        return 200, self._task_runs

    def delete_dispatch(self, project_id: str) -> int:
        self.deleted.append(project_id)
        return 204


def _three_handles() -> dict[str, DispatchHandle]:
    return {
        "scratch": _stage_handle("proj-1", "env-1", "issue-1", "sbx-1"),
        "branch": _stage_handle("proj-2", "env-2", "issue-2", "sbx-2"),
        "resume": _stage_handle("proj-3", "env-3", "issue-3", "sbx-3"),
    }


def _exec_router(
    calls: list[str],
    *,
    lineage_ok: bool = True,
    acceptance_ok: bool = True,
):
    """Fake run_in_sandbox routing lineage vs acceptance commands."""

    def fake(
        proxy: str, domain: str, sandbox_id: str, cmd: str, timeout: int
    ) -> ExecResult:
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

    return fake


def test_chain_threads_env_ids_and_passes(monkeypatch: pytest.MonkeyPatch) -> None:
    exec_calls: list[str] = []
    monkeypatch.setattr(
        chain.sandbox_exec, "run_in_sandbox", _exec_router(exec_calls)
    )
    client = ChainFakeClient(handles=_three_handles())
    chain_run = run_chain(_config(), client)

    assert isinstance(chain_run, ChainRun)
    assert len(chain_run.stages) == 3
    assert [s.stage for s in chain_run.stages] == ["scratch", "branch", "resume"]
    assert all(s.passed for s in chain_run.stages)
    assert chain_run.outcome == "passed"

    # env_id threading: branch forks scratch's rollout env, resume forks
    # branch's rollout env; no issue payload at lineage stages.
    assert client.scratch_calls == 1
    assert client.branch_calls == [("env-1", "branch"), ("env-2", "resume")]

    # Lineage evaluated at both lineage stages; acceptance green everywhere.
    assert chain_run.stages[1].lineage_ok is True
    assert chain_run.stages[2].lineage_ok is True
    assert all(s.acceptance_ok is True for s in chain_run.stages)

    # Exec budget: scratch acceptance (1) + per lineage stage (2 lineage +
    # 1 acceptance) = 7 calls.
    assert len(exec_calls) == 7


def test_chain_lineage_failure_stops_chain_without_waiting(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    exec_calls: list[str] = []
    monkeypatch.setattr(
        chain.sandbox_exec,
        "run_in_sandbox",
        _exec_router(exec_calls, lineage_ok=False),
    )
    client = ChainFakeClient(handles=_three_handles())
    chain_run = run_chain(_config(), client)

    # Chain stops at the branch stage; resume is never submitted (US3
    # scenario 3); earlier stage results are preserved.
    assert len(chain_run.stages) == 2
    assert client.branch_calls == [("env-1", "branch")]
    scratch, branch = chain_run.stages
    assert scratch.passed

    assert branch.lineage_ok is False
    assert branch.phase == PHASE_VERIFICATION
    assert branch.error is not None and "lineage" in branch.error
    assert branch.failed_lineage_markers
    # FR-016: failed BEFORE waiting on the agent — the branch DAG was never
    # polled.
    assert "proj-2" not in client.dag_calls_by_project
    assert chain_run.outcome == "failed"


def test_chain_stops_when_scratch_fails(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(
        chain.sandbox_exec,
        "run_in_sandbox",
        _exec_router([], acceptance_ok=False),
    )
    client = ChainFakeClient(handles=_three_handles())
    chain_run = run_chain(_config(), client)

    assert len(chain_run.stages) == 1
    assert client.branch_calls == []
    assert chain_run.stages[0].acceptance_ok is False
    assert chain_run.outcome == "failed"


def test_chain_timeout_outcome_propagates(monkeypatch: pytest.MonkeyPatch) -> None:
    clock = itertools.chain([0.0, 0.0], itertools.cycle([2000.0]))
    monkeypatch.setattr(chain.time, "monotonic", lambda: next(clock))
    client = ChainFakeClient(
        handles=_three_handles(),
        dag_by_project={"proj-1": [(202, {"status": "in_progress"})]},
        task_runs=[{"status": "running"}],
    )
    chain_run = run_chain(_config(), client)
    assert len(chain_run.stages) == 1
    assert chain_run.stages[0].dag_status == DAG_TIMEOUT
    assert chain_run.outcome == "timeout"


def test_lineage_stage_rejects_unpassed_previous_stage() -> None:
    client = ChainFakeClient(handles=_three_handles())
    failed_prev = StageResult(stage="scratch", mode_sent="scratch", dispatch=None)
    failed_prev.error = "boom"
    result = chain.run_lineage_stage(_config(), client, failed_prev, "branch")
    assert not result.passed
    assert result.error is not None and "did not pass" in result.error
    assert client.branch_calls == []
