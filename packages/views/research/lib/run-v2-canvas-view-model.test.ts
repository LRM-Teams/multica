import { describe, expect, it } from "vitest";
import type { ResearchGraphNode, ResearchRunSnapshot } from "@multica/core/types";
import { buildRunV2CanvasViewModel } from "./run-v2-canvas-view-model";

const baseNode = (id: string, kind: string, entityId: string): ResearchGraphNode => ({
  id,
  session_id: "session",
  node_type: kind === "question" ? "subquestion" : "probe",
  title: id,
  summary: "",
  status: "running",
  actor_agent_id: null,
  payload: { projection: "run_v2", kind, [`${kind}_id`]: entityId },
  created_at: "2026-08-04T00:00:00Z",
  updated_at: "2026-08-04T00:00:00Z",
});

const run = {
  questions: [{ id: "q1" }],
  tasks: [{ id: "t1", status: "running", assigned_agent_id: "agent-1", started_at: "2026-08-04T00:00:00Z" }],
  attempts: [{ id: "a1", task_id: "t1", attempt_number: 1, assigned_agent_id: "agent-1", status: "failed", diagnostics: "source timed out", started_at: "2026-08-04T00:01:00Z" }],
  gate: { passed: false, findings: [{ code: "tasks_incomplete", severity: "error", message: "16 tasks incomplete", metadata: { task_id: "t1" } }] },
} as unknown as ResearchRunSnapshot;

describe("buildRunV2CanvasViewModel", () => {
  it("enriches projected task cards and makes gate findings locate their node", () => {
    const nodes = [baseNode("root", "root", "session"), baseNode("question", "question", "q1"), baseNode("task", "task", "t1")];
    const model = buildRunV2CanvasViewModel(nodes, run, [{ agent_id: "agent-1", display_name: "Lin" } as never], Date.parse("2026-08-04T00:09:00Z"));
    expect(model.nodes[0]).toBe(nodes[0]);
    const taskNode = model.nodes[2];
    if (!taskNode) throw new Error("expected task node at index 2");
    expect(taskNode.payload).toMatchObject({ execution: { agent: "Lin", status: "failed", duration: "8 min", failure: "source timed out" } });
    expect(model.blockers).toEqual([{ id: "tasks_incomplete:0", label: "16 tasks incomplete", targetNodeId: "task" }]);
    expect(model.degraded).toBe(false);
  });

  it("reports an explicit degraded state without manufacturing topology", () => {
    const model = buildRunV2CanvasViewModel([], run, []);
    expect(model.nodes).toEqual([]);
    expect(model.degraded).toBe(true);
  });
});
