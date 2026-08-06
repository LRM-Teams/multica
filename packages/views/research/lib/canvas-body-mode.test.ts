// @vitest-environment node
import { describe, expect, it } from "vitest";
import { resolveCanvasBodyMode } from "./canvas-body-mode";

const node = (id: string, node_type: string) => ({ id, node_type });
const edge = (from_node_id: string, to_node_id: string) => ({ from_node_id, to_node_id });

describe("resolveCanvasBodyMode (semantic forming)", () => {
  it("keeps goal-only and event-only scaffolding in forming", () => {
    expect(resolveCanvasBodyMode({ nodes: [node("goal", "goal")], edges: [], sessionStatus: "running" })).toBe("forming");
    expect(resolveCanvasBodyMode({ nodes: [node("activity", "agent_activity"), node("gate", "stage_gate")], edges: [edge("activity", "gate")], sessionStatus: "running" })).toBe("forming");
  });

  it("ignores dangling edges and enters ready on the first valid business edge", () => {
    const nodes = [node("goal", "goal"), node("finding", "finding")];
    expect(resolveCanvasBodyMode({ nodes, edges: [edge("missing", "finding")], sessionStatus: "running" })).toBe("forming");
    expect(resolveCanvasBodyMode({ nodes, edges: [edge("goal", "finding")], sessionStatus: "running" })).toBe("ready");
  });

  it("keeps loading, forming, stalled, and empty distinct", () => {
    const base = { nodes: [node("goal", "goal")], edges: [] };
    expect(resolveCanvasBodyMode({ ...base, loading: true })).toBe("loading");
    expect(resolveCanvasBodyMode({ ...base, sessionStatus: "running" })).toBe("forming");
    expect(resolveCanvasBodyMode({ ...base, sessionStatus: "paused" })).toBe("stalled");
    expect(resolveCanvasBodyMode({ ...base, sessionStatus: "completed" })).toBe("empty");
  });
});
