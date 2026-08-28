import { describe, expect, it } from "vitest";
import type {
  ProblemEvolutionCandidate,
  ProblemEvolutionCandidateEdge,
} from "@multica/core/problem-evolution";
import { groupCandidatesByLane, indexParentsByChild } from "./problem-evolution-run-canvas";

function candidate(
  id: string,
  externalRef: string,
  lane = "baseline",
): ProblemEvolutionCandidate {
  return {
    id,
    run_id: "run",
    external_ref: externalRef,
    generation: 0,
    lane,
    operator: "baseline",
    status: "selectable",
    feasible: true,
    feedback_rounds: 0,
    artifact_ref: "",
    artifact_hash: "",
    summary: "",
    change_summary: "",
    failure_class: "",
    runtime_seconds: 0,
    cost: 0,
    created_at: "",
    updated_at: "",
  };
}

function edge(
  parentID: string,
  childID: string,
  relation: string,
  parentIndex: number,
): ProblemEvolutionCandidateEdge {
  return {
    parent_id: parentID,
    child_id: childID,
    relation,
    parent_index: parentIndex,
  };
}

describe("indexParentsByChild", () => {
  it("orders crossover parents by parent_index", () => {
    const candidates = [
      candidate("id-p1", "p1"),
      candidate("id-p2", "p2"),
      candidate("id-child", "child"),
    ];
    const lineage = indexParentsByChild(candidates, [
      edge("id-p2", "id-child", "crossover_of", 1),
      edge("id-p1", "id-child", "crossover_of", 0),
    ]);
    expect(lineage.get("id-child")).toEqual({
      relation: "crossover_of",
      parentRefs: ["p1", "p2"],
    });
  });

  it("drops edges whose parent is not in the snapshot", () => {
    // A snapshot can be paged or filtered; a dangling edge must not render as
    // an "undefined" parent.
    const lineage = indexParentsByChild([candidate("id-child", "child")], [
      edge("id-missing", "id-child", "repair_of", 0),
    ]);
    expect(lineage.size).toBe(0);
  });
});

describe("groupCandidatesByLane", () => {
  it("keeps lane order stable regardless of arrival order", () => {
    const first = groupCandidatesByLane([
      candidate("a", "a", "repair"),
      candidate("b", "b", "crossover"),
    ]).map(([lane]) => lane);
    const second = groupCandidatesByLane([
      candidate("b", "b", "crossover"),
      candidate("a", "a", "repair"),
    ]).map(([lane]) => lane);
    expect(first).toEqual(second);
  });
});
