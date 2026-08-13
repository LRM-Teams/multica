import { describe, expect, it } from "vitest";
import type { TypedGraphNode } from "@multica/core/research";
import type { ExecutionRow } from "../execution-overlay";
import { resolveAgentInspectorRow } from "./resolve-agent-inspector-row";

const node = {
  id: "node-s-1",
  title: "Source verification",
  level: "S",
  actor_agent_id: "agent-archived",
} as TypedGraphNode;

describe("resolveAgentInspectorRow", () => {
  it("uses the authoritative projected execution row when available", () => {
    const row = {
      id: "agent-archived",
      name: "Ada",
      role: "researcher",
      initials: "AD",
      status: "done",
      actionKey: "recent_done",
    } as ExecutionRow;

    expect(resolveAgentInspectorRow([row], node)).toBe(row);
  });

  it("keeps an S node inspectable when its actor is absent from the current fleet", () => {
    expect(resolveAgentInspectorRow([], node)).toEqual({
      id: "agent-archived",
      name: "agent-archived",
      role: "Agent",
      initials: "AG",
      status: "unknown",
      actionKey: "unknown",
      currentNodeId: "node-s-1",
      locationLabel: "Source verification",
    });
  });

  it("does not fabricate an inspector without a canonical actor", () => {
    expect(
      resolveAgentInspectorRow([], { ...node, actor_agent_id: null }),
    ).toBeNull();
  });
});
