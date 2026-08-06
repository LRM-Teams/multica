"""Failure diagnostics for the env-dispatch issue E2E (US4, T025).

`FailureReport` bundles everything needed to triage a non-pass chain run
(data-model.md "FailureReport"): the failing chain stage + phase, the raw
submit response, the last DAG poll, the task-runs terminal detail, and the
per-stage cleanup outcomes. `render()` produces the stdout triage block
described in quickstart.md "Failure triage".
"""

from __future__ import annotations

import json
from dataclasses import dataclass, field
from typing import TYPE_CHECKING, Any

if TYPE_CHECKING:  # avoid the import cycle: chain.py imports this module
    from e2e_harness.chain import ChainRun

_SNAPSHOT_TRUNCATE = 800

_PHASE_HINTS = {
    "submit": (
        "dispatch rejected or rollout error — inspect dispatch_response "
        "(base env / squad / workspace validity, per-rollout error)"
    ),
    "reset": "environment reset/fork did not complete — inspect dag_snapshot",
    "pickup": "issue never picked up by the squad — no task activity",
    "execution": "agent picked up the issue but the run failed — worked but failed",
    "verification": (
        "terminal outcome alone is never sufficient (FR-006): the "
        "harness-executed checks decided — see summary/failed checks"
    ),
}


@dataclass
class FailureReport:
    """Diagnostics bundle emitted on any non-pass outcome (FR-007)."""

    stage: str
    phase: str
    summary: str
    dispatch_response: Any = None
    dag_snapshot: Any = None
    task_snapshot: Any = None
    messages_tail: list[str] = field(default_factory=list)
    cleanup_results: list[str] = field(default_factory=list)

    def _triage_hints(self) -> list[str]:
        hints = [_PHASE_HINTS.get(self.phase, "inspect the snapshots above")]
        task_text = str(self.task_snapshot or "")
        if "never picked up" in task_text:
            hints.append(
                "task-runs show no activity: the agent NEVER PICKED UP the "
                "issue (scheduling/squad problem, not agent output)"
            )
        elif "error=" in task_text or "failure_reason=" in task_text:
            hints.append(
                "task-runs show the agent WORKED BUT FAILED: see error/"
                "failure_reason in task_snapshot"
            )
        if self.phase == "verification" and self.stage != "scratch":
            hints.append(
                f"failure at lineage stage {self.stage!r}: a missing prior-"
                "stage change is a FORK REGRESSION in the product, not a "
                "test flake — investigate the server fork path"
            )
        return hints

    @staticmethod
    def _fmt(value: Any) -> str:
        if value is None:
            return "(none captured)"
        if isinstance(value, str):
            return value
        try:
            rendered = json.dumps(value, indent=2, default=str)
        except (TypeError, ValueError):
            rendered = str(value)
        if len(rendered) > _SNAPSHOT_TRUNCATE:
            rendered = rendered[:_SNAPSHOT_TRUNCATE] + "… <truncated>"
        return rendered

    def render(self) -> str:
        """Stdout triage block (quickstart.md "Failure triage")."""
        lines = [
            "================ E2E FAILURE TRIAGE ================",
            f"stage            : {self.stage}",
            f"phase            : {self.phase}",
            f"summary          : {self.summary}",
            "--- dispatch_response (raw submit response / HTTP error) ---",
            self._fmt(self.dispatch_response),
            "--- dag_snapshot (last DAG poll) ---",
            self._fmt(self.dag_snapshot),
            "--- task_snapshot (task-runs terminal detail) ---",
            self._fmt(self.task_snapshot),
            "--- messages_tail ---",
            "\n".join(self.messages_tail) if self.messages_tail else "(none captured)",
            "--- cleanup_results (per-stage DELETE) ---",
            "\n".join(self.cleanup_results)
            if self.cleanup_results
            else "(nothing submitted)",
            "--- triage hints ---",
            *(f"- {hint}" for hint in self._triage_hints()),
            "====================================================",
        ]
        return "\n".join(lines)


def build_failure_report(
    chain_run: ChainRun,
    cleanup_results: list[str] | None = None,
) -> FailureReport | None:
    """Build the report for the first failing stage of a chain run."""
    failing = next((s for s in chain_run.stages if not s.passed), None)
    if failing is None:
        return None
    if failing.dispatch is not None:
        dispatch_response: Any = failing.dispatch.raw
    else:
        dispatch_response = failing.dispatch_response
    return FailureReport(
        stage=failing.stage,
        phase=failing.phase,
        summary=failing.error or "stage failed without an error message",
        dispatch_response=dispatch_response,
        dag_snapshot=failing.dag_snapshot,
        task_snapshot=failing.task_status,
        cleanup_results=list(cleanup_results or []),
    )
