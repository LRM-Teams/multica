"""Chain orchestration for the env-dispatch issue E2E (US1/US2/US3).

One chain = three dispatch stages sharing one environment lineage:
`scratch` -> `branch` -> `resume`. Stage runners: `run_scratch_stage`
(T014), `run_lineage_stage` for branch/resume (T021), and the `run_chain`
orchestrator (T022). IssueSpec construction (T015) and the harness-executed
acceptance verification (T018) are shared by all stages.

State machine (data-model.md):

    submit --> reset --> pickup --> execution --> verification --> PASSED

Each phase can fail or hit the stage timeout. Agent self-reported results
are never accepted as verdicts (FR-006): a stage passes only when the DAG
assembled with >= 1 segment AND the harness-run acceptance checks exit 0
inside the stage's leader sandbox. At branch/resume stages the lineage
checks run FIRST — right after submit, before waiting on the agent — and a
missing prior-stage change fails the stage at the verification phase
immediately (FR-016: a fork regression is a product bug, not a flake).
"""

from __future__ import annotations

import time
import uuid
from dataclasses import dataclass, field
from typing import Any, Protocol

from e2e_harness import sandbox_exec
from e2e_harness.config import HarnessConfig
from e2e_harness.diagnostics import FailureReport, build_failure_report
from e2e_harness.multica_client import DispatchHandle, MulticaAPIError

# Fixture repo path inside the sandbox (contracts/sandbox-exec.md caveat;
# written there by provision_fixture.py).
FIXTURE_REPO_PATH = "/workspace/repo"

# Acceptance check node ids — must match fixture_repo tests (T010/T015).
FIXTURE_FAIL_TO_PASS: list[str] = [
    "tests/test_calculator.py::test_add_integers",
    "tests/test_calculator.py::test_add_negative",
    "tests/test_calculator.py::test_add_floats",
]
FIXTURE_PASS_TO_PASS: list[str] = ["tests/test_sanity.py"]

# Lineage marker file the issue instructs the agent to create (T010 README).
LINEAGE_MARKER_FILE = "SOLUTION.md"

# Lineage checks (FR-016, data-model.md "LineageCheck"): concrete proof that
# a forked environment contains the prior stages' work. Evaluated at the
# verification phase, ALWAYS before crediting the agent's terminal outcome.
# Each entry: (marker name, bash command that must exit 0).
LINEAGE_CHECKS: list[tuple[str, str]] = [
    (
        f"{LINEAGE_MARKER_FILE} present at repo root",
        f"cd {FIXTURE_REPO_PATH} && test -f {LINEAGE_MARKER_FILE}",
    ),
    (
        "fixture_calc.calculator.add implemented",
        f"cd {FIXTURE_REPO_PATH} && PYTHONPATH=src python -c "
        '"from fixture_calc.calculator import add; assert add(2, 3) == 5"',
    ),
]

ACCEPTANCE_EXEC_TIMEOUT_SEC = 300
LINEAGE_EXEC_TIMEOUT_SEC = 60

# Negative control (FR-010, T027): when E2E_NEGATIVE_CONTROL=1 the acceptance
# command is swapped for this unsatisfiable check — the run MUST fail at the
# verification phase, proving the suite is not vacuously green (SC-003).
NEGATIVE_CONTROL_CHECK = 'python -c "import sys; sys.exit(1)"'

# Stage names / wire modes.
STAGE_SCRATCH = "scratch"
STAGE_BRANCH = "branch"
STAGE_RESUME = "resume"

# Stage phases (data-model.md state machine).
PHASE_SUBMIT = "submit"
PHASE_RESET = "reset"
PHASE_PICKUP = "pickup"
PHASE_EXECUTION = "execution"
PHASE_VERIFICATION = "verification"
_PHASE_ORDER = [
    PHASE_SUBMIT,
    PHASE_RESET,
    PHASE_PICKUP,
    PHASE_EXECUTION,
    PHASE_VERIFICATION,
]

# DAG poll outcomes.
DAG_IN_PROGRESS = "in_progress"
DAG_ASSEMBLED = "assembled"
DAG_FAILED = "failed"
DAG_TIMEOUT = "timeout"

# Chain outcomes (data-model.md "ChainRun").
OUTCOME_PASSED = "passed"
OUTCOME_FAILED = "failed"
OUTCOME_TIMEOUT = "timeout"

# Task statuses meaning "created but the agent is not acting on it yet".
_IDLE_TASK_STATUSES = {"", "pending", "queued", "backlog"}


class StageClient(Protocol):
    """The subset of MulticaClient the stage runners need (mocked in tests)."""

    def dispatch_scratch(self, issue_spec: dict[str, Any]) -> DispatchHandle: ...

    def dispatch_branch(self, env_id: str, mode: str) -> DispatchHandle: ...

    def get_dag(self, project_id: str) -> tuple[int, Any]: ...

    def get_task_runs(self, issue_id: str) -> tuple[int, Any]: ...

    def delete_dispatch(self, project_id: str) -> int: ...


@dataclass
class StageResult:
    """One dispatch within the chain (data-model.md "StageResult").

    The snapshot fields (dispatch_response / dag_snapshot / task_snapshot)
    feed FailureReport diagnostics on any non-pass outcome.
    """

    stage: str
    mode_sent: str
    dispatch: DispatchHandle | None
    phase: str = PHASE_SUBMIT
    dag_status: str = DAG_IN_PROGRESS
    task_status: str | None = None
    lineage_ok: bool | None = None
    acceptance_ok: bool | None = None
    segments: int = 0
    duration_sec: float = 0.0
    error: str | None = None
    failed_checks: list[str] = field(default_factory=list)
    failed_lineage_markers: list[str] = field(default_factory=list)
    acceptance_sandbox_id: str | None = None
    dispatch_response: Any = None
    dag_snapshot: Any = None
    task_snapshot: Any = None

    @property
    def passed(self) -> bool:
        return (
            self.error is None
            and self.dag_status == DAG_ASSEMBLED
            and self.segments >= 1
            and self.acceptance_ok is True
            and (self.lineage_ok in (True, None))
        )


@dataclass
class ChainRun:
    """One E2E invocation: ordered stages sharing one lineage (data-model.md).

    `failure` carries the FailureReport (T025/T026) on any non-pass outcome.
    """

    run_id: str
    stages: list[StageResult]
    negative_control: bool = False
    failure: FailureReport | None = None

    @property
    def outcome(self) -> str:
        if (
            len(self.stages) == 3
            and all(stage.passed for stage in self.stages)
        ):
            return OUTCOME_PASSED
        if any(stage.dag_status == DAG_TIMEOUT for stage in self.stages):
            return OUTCOME_TIMEOUT
        return OUTCOME_FAILED


def build_issue_spec(run_id: str | None = None) -> dict[str, Any]:
    """Build the chain's single IssueSpec (T015, FR-017).

    Submitted only at the scratch stage; branch/resume copy it server-side.
    All checks assert end-state properties so re-verification at later stages
    is idempotent.
    """
    if run_id is None:
        run_id = uuid.uuid4().hex[:12]
    return {
        "title": f"E2E fixture: implement fixture_calc.calculator.add [{run_id}]",
        "description": (
            f"In the repository at {FIXTURE_REPO_PATH} (package `fixture-calc`), "
            "implement the function `add(a, b)` in "
            "src/fixture_calc/calculator.py so it returns the arithmetic sum "
            "of its two arguments (ints and floats). It currently raises "
            f"NotImplementedError. Also create a file {LINEAGE_MARKER_FILE} "
            "at the repository root briefly describing the change. Do not "
            "modify the tests."
        ),
        "acceptance_criteria": [
            "`python -m pytest tests/test_calculator.py -q` exits 0 "
            "(add() returns a + b for ints and floats)",
            "`python -m pytest tests/test_sanity.py -q` still exits 0",
            f"{LINEAGE_MARKER_FILE} exists at the repository root",
        ],
        "fail_to_pass": list(FIXTURE_FAIL_TO_PASS),
        "pass_to_pass": list(FIXTURE_PASS_TO_PASS),
    }


def _acceptance_spec() -> dict[str, Any]:
    """Check node ids for branch/resume stages (issue copied server-side)."""
    return {
        "fail_to_pass": list(FIXTURE_FAIL_TO_PASS),
        "pass_to_pass": list(FIXTURE_PASS_TO_PASS),
    }


def _advance_phase(current: str, target: str) -> str:
    """Move the phase forward only (never regress execution -> pickup)."""
    if _PHASE_ORDER.index(target) > _PHASE_ORDER.index(current):
        return target
    return current


def _normalize_task_runs(body: Any) -> list[dict[str, Any]]:
    """task-runs returns a JSON array of AgentTaskResponse; tolerate wrappers."""
    if isinstance(body, list):
        return [t for t in body if isinstance(t, dict)]
    if isinstance(body, dict):
        for key in ("tasks", "task_runs", "runs"):
            value = body.get(key)
            if isinstance(value, list):
                return [t for t in value if isinstance(t, dict)]
    return []


def _summarize_task_runs(tasks: list[dict[str, Any]]) -> str:
    """Terminal detail distinguishing never-picked-up from worked-but-failed."""
    if not tasks:
        return "no task runs (issue never picked up)"
    parts: list[str] = []
    for task in tasks:
        status = task.get("status") or "unknown"
        detail = f"status={status}"
        if task.get("error"):
            detail += f" error={str(task['error'])[:200]}"
        if task.get("failure_reason"):
            detail += f" failure_reason={str(task['failure_reason'])[:200]}"
        parts.append(detail)
    return f"{len(tasks)} task run(s): " + "; ".join(parts)


def _parse_failed_checks(stdout: str) -> list[str]:
    """Extract failed pytest node ids from the short summary (FAILED lines)."""
    failed: list[str] = []
    for line in stdout.splitlines():
        line = line.strip()
        if line.startswith("FAILED "):
            node = line[len("FAILED ") :].split(" - ", 1)[0].strip()
            if node:
                failed.append(node)
    return failed


def _run_acceptance(
    config: HarnessConfig,
    handle: DispatchHandle,
    issue_spec: dict[str, Any],
) -> tuple[bool, list[str], str | None, str | None]:
    """Run the issue's checks in the leader sandbox; judge by exit code (T018).

    Returns (acceptance_ok, failed_checks, sandbox_id, error_message).
    """
    sandbox_id = handle.leader_sandbox_id
    if not sandbox_id:
        return (
            False,
            [],
            None,
            "no leader sandbox id in the dispatch response "
            "(agent_sandbox_refs/sandbox_refs empty)",
        )
    nodes = list(issue_spec.get("fail_to_pass") or []) + list(
        issue_spec.get("pass_to_pass") or []
    )
    if not nodes:
        return False, [], sandbox_id, "issue spec carries no checks to run"
    if config.negative_control:
        # FR-010: unsatisfiable check — the run MUST fail here.
        cmd = f"cd {FIXTURE_REPO_PATH} && {NEGATIVE_CONTROL_CHECK}"
    else:
        cmd = f"cd {FIXTURE_REPO_PATH} && python -m pytest {' '.join(nodes)} -q"
    result = sandbox_exec.run_in_sandbox(
        config.cube_proxy_url,
        config.cube_domain,
        sandbox_id,
        cmd,
        ACCEPTANCE_EXEC_TIMEOUT_SEC,
    )
    if result.transport_error is not None:
        # Transport failure is a verification failure — never silently skipped.
        return (
            False,
            [],
            sandbox_id,
            f"acceptance check transport error: {result.transport_error}",
        )
    if result.ok:
        return True, [], sandbox_id, None
    failed = _parse_failed_checks(result.stdout)
    prefix = "negative-control check failed (expected): " if config.negative_control else ""
    if failed:
        message = prefix + "acceptance checks failed: " + ", ".join(failed)
    else:
        message = prefix + (
            f"acceptance command exited {result.exit_code} "
            f"(could not parse failed checks): {result.stdout[-300:]}"
        )
    return False, failed, sandbox_id, message


def _run_lineage_checks(
    config: HarnessConfig,
    handle: DispatchHandle,
) -> tuple[bool, list[str], str | None]:
    """Run the lineage markers in the forked leader sandbox (T021, FR-016).

    Returns (lineage_ok, failed_markers, sandbox_id). Transport errors fail
    the lineage check like a missing marker — never silently skipped.
    """
    sandbox_id = handle.leader_sandbox_id
    if not sandbox_id:
        return False, ["no leader sandbox id in the dispatch response"], None
    failed: list[str] = []
    for name, cmd in LINEAGE_CHECKS:
        result = sandbox_exec.run_in_sandbox(
            config.cube_proxy_url,
            config.cube_domain,
            sandbox_id,
            cmd,
            LINEAGE_EXEC_TIMEOUT_SEC,
        )
        if result.transport_error is not None:
            failed.append(f"{name} (transport error: {result.transport_error})")
        elif not result.ok:
            failed.append(name)
    return (not failed), failed, sandbox_id


def _submit(
    result: StageResult,
    dispatch_fn: Any,
    started: float,
) -> DispatchHandle | None:
    """Submit phase: fail-fast on HTTP or per-rollout error (data-model.md).

    On failure, records the error on `result` and returns None; on success
    returns the handle (also stored on result.dispatch for caller cleanup).
    """
    try:
        handle = dispatch_fn()
    except MulticaAPIError as exc:
        result.error = f"submit failed: {exc}"
        result.dispatch_response = {"status": exc.status, "body": exc.body}
        result.duration_sec = time.monotonic() - started
        return None
    result.dispatch = handle
    result.dispatch_response = handle.raw
    if not handle.submit_ok:
        result.error = (
            "submit failed: rollout reported an error "
            f"(agent_run_id={handle.agent_run_id!r}): {handle.rollout_error}"
        )
        result.duration_sec = time.monotonic() - started
        return None
    return handle


def _wait_for_dag(
    config: HarnessConfig,
    client: StageClient,
    result: StageResult,
    handle: DispatchHandle,
) -> None:
    """Phases reset/pickup/execution: poll the DAG until terminal or timeout.

    On return, result.dag_status is assembled/failed/timeout; on non-assembled
    outcomes result.error is set (with task-runs terminal detail appended).
    """
    result.phase = PHASE_RESET
    deadline = time.monotonic() + config.stage_timeout_sec
    while True:
        if time.monotonic() >= deadline:
            result.dag_status = DAG_TIMEOUT
            result.error = (
                f"stage timeout after {config.stage_timeout_sec}s "
                f"in phase {result.phase}"
            )
            break
        try:
            status, body = client.get_dag(handle.project_id)
        except MulticaAPIError as exc:
            result.dag_status = DAG_FAILED
            result.error = f"DAG poll transport error in phase {result.phase}: {exc}"
            break
        result.dag_snapshot = {"status": status, "body": body}

        if status == 200 and isinstance(body, dict):
            if body.get("status") == "failed":
                result.dag_status = DAG_FAILED
                result.error = f"DAG assembly failed: {str(body)[:500]}"
            else:
                segments = body.get("segments") or []
                result.segments = len(segments)
                result.dag_status = DAG_ASSEMBLED
            break
        if status in (403, 404):
            result.dag_status = DAG_FAILED
            result.error = f"DAG poll returned {status}: {str(body)[:300]}"
            break

        # 202 (or any other status): still running — track pickup/execution.
        if handle.issue_id:
            try:
                _, runs_body = client.get_task_runs(handle.issue_id)
            except MulticaAPIError:
                runs_body = []
            tasks = _normalize_task_runs(runs_body)
            if tasks:
                result.phase = _advance_phase(result.phase, PHASE_PICKUP)
                if any(
                    str(t.get("status") or "").lower() not in _IDLE_TASK_STATUSES
                    for t in tasks
                ):
                    result.phase = _advance_phase(result.phase, PHASE_EXECUTION)
        time.sleep(config.dag_poll_interval_sec)

    # Terminal detail: never-picked-up vs worked-but-failed.
    if handle.issue_id:
        try:
            _, runs_body = client.get_task_runs(handle.issue_id)
            tasks = _normalize_task_runs(runs_body)
        except MulticaAPIError as exc:
            result.task_status = f"task-runs unavailable: {exc}"
        else:
            result.task_snapshot = runs_body
            result.task_status = _summarize_task_runs(tasks)
    if result.dag_status != DAG_ASSEMBLED and result.error:
        result.error = f"{result.error} | {result.task_status}"


def _wait_and_verify(
    config: HarnessConfig,
    client: StageClient,
    result: StageResult,
    handle: DispatchHandle,
    issue_spec: dict[str, Any],
    started: float,
) -> None:
    """Shared wait -> DAG assert -> acceptance flow (T014/T018 internals)."""
    _wait_for_dag(config, client, result, handle)
    if result.dag_status != DAG_ASSEMBLED:
        result.duration_sec = time.monotonic() - started
        return

    result.phase = PHASE_VERIFICATION
    if result.segments < 1:
        result.error = (
            f"DAG assembled with {result.segments} segments (need >= 1) | "
            f"{result.task_status}"
        )
        result.duration_sec = time.monotonic() - started
        return

    acceptance_ok, failed_checks, sandbox_id, error = _run_acceptance(
        config, handle, issue_spec
    )
    result.acceptance_ok = acceptance_ok
    result.failed_checks = failed_checks
    result.acceptance_sandbox_id = sandbox_id
    if not acceptance_ok:
        result.error = f"{error} | {result.task_status}"
    result.duration_sec = time.monotonic() - started


def run_scratch_stage(
    config: HarnessConfig,
    client: StageClient,
    issue_spec: dict[str, Any],
) -> StageResult:
    """Run the scratch stage (T014): submit -> wait -> DAG assert -> verify.

    Never raises for remote failures; all outcomes land on the StageResult.
    Cleanup (DELETE) is the caller's job so it also runs on failure.
    """
    started = time.monotonic()
    result = StageResult(stage=STAGE_SCRATCH, mode_sent=STAGE_SCRATCH, dispatch=None)
    handle = _submit(result, lambda: client.dispatch_scratch(issue_spec), started)
    if handle is None:
        return result
    _wait_and_verify(config, client, result, handle, issue_spec, started)
    return result


def run_lineage_stage(
    config: HarnessConfig,
    client: StageClient,
    prev_stage: StageResult,
    mode: str,
) -> StageResult:
    """Run a branch/resume stage (T021).

    Dispatches `mode` from the previous stage's rollout env (no issue
    payload — the server copies it), then runs the lineage checks FIRST
    (FR-016): a missing prior-stage change fails the stage at the
    verification phase WITHOUT waiting on the agent. Lineage green => the
    same wait/DAG/acceptance flow as scratch.
    """
    if mode not in (STAGE_BRANCH, STAGE_RESUME):
        raise ValueError(f"mode must be {STAGE_BRANCH!r} or {STAGE_RESUME!r}")
    started = time.monotonic()
    result = StageResult(stage=mode, mode_sent=mode, dispatch=None)

    prev = prev_stage.dispatch
    if prev is None or not prev_stage.passed or not prev.env_id:
        result.error = (
            f"cannot {mode}: previous stage {prev_stage.stage!r} did not pass"
        )
        result.duration_sec = time.monotonic() - started
        return result

    handle = _submit(
        result, lambda: client.dispatch_branch(prev.env_id, mode), started
    )
    if handle is None:
        return result

    # FR-016: lineage FIRST, at the verification phase, before any waiting.
    result.phase = PHASE_VERIFICATION
    lineage_ok, failed_markers, _sandbox_id = _run_lineage_checks(config, handle)
    result.lineage_ok = lineage_ok
    result.failed_lineage_markers = failed_markers
    if not lineage_ok:
        result.error = (
            f"lineage check failed at stage {mode}: missing prior-stage "
            f"changes: {', '.join(failed_markers)}"
        )
        result.duration_sec = time.monotonic() - started
        return result

    _wait_and_verify(config, client, result, handle, _acceptance_spec(), started)
    return result


def _cleanup_stages(client: StageClient, stages: list[StageResult]) -> list[str]:
    """DELETE every submitted stage's dispatch project (T026, FR-008).

    204 is idempotent (missing row also 204). Cleanup errors are recorded —
    never raised — so they cannot mask the chain's actual result (spec edge
    case).
    """
    results: list[str] = []
    for stage in stages:
        handle = stage.dispatch
        if handle is None or not handle.project_id:
            continue
        try:
            status = client.delete_dispatch(handle.project_id)
        except MulticaAPIError as exc:
            results.append(
                f"{stage.stage}/{handle.project_id}: cleanup error "
                f"(logged, not masking result): {exc}"
            )
        else:
            results.append(f"{stage.stage}/{handle.project_id}: DELETE {status}")
    return results


def run_chain(config: HarnessConfig, client: StageClient) -> ChainRun:
    """Orchestrate scratch -> branch -> resume (T022).

    Threads `rollouts[0].env_id` of each stage into the next dispatch and
    stops at the first failing stage (FR-014 single attempt, fail-fast);
    earlier stage results are always preserved on the ChainRun. Cleanup is
    guaranteed via try/finally (T026): every submitted stage's project is
    DELETEd regardless of outcome, and any non-pass outcome carries a
    FailureReport for the pytest failure message.
    """
    run_id = uuid.uuid4().hex[:12]
    stages: list[StageResult] = []
    cleanup_results: list[str] = []
    try:
        scratch = run_scratch_stage(config, client, build_issue_spec(run_id))
        stages.append(scratch)
        if scratch.passed:
            branch = run_lineage_stage(config, client, scratch, STAGE_BRANCH)
            stages.append(branch)
            if branch.passed:
                stages.append(run_lineage_stage(config, client, branch, STAGE_RESUME))
    finally:
        cleanup_results = _cleanup_stages(client, stages)

    chain_run = ChainRun(
        run_id=run_id,
        stages=stages,
        negative_control=config.negative_control,
    )
    if chain_run.outcome != OUTCOME_PASSED:
        chain_run.failure = build_failure_report(chain_run, cleanup_results)
    return chain_run
