import { describe, expect, it } from "vitest";
import type { TypedGraphResponse } from "@multica/core/research";
import { buildTypedGraphMotionEvents } from "./build-typed-graph-motion-events";

const base = {
  session_id: "s1",
  graph_version: 1,
  nodes: [{ id: "a", title: "A", status: "running", level: "s" }],
  edges: [],
  clusters: [],
  lineage: {
    derived: {},
    merged: {},
    superseded: {},
    restarted: {},
    invalidated: {},
    supersedes: {},
  },
} as unknown as TypedGraphResponse;

describe("buildTypedGraphMotionEvents", () => {
  it("emits branch_spawned for new nodes", () => {
    const next = {
      ...base,
      graph_version: 2,
      nodes: [...base.nodes, { id: "b", title: "B", status: "running", level: "s" }],
    } as unknown as TypedGraphResponse;

    const events = buildTypedGraphMotionEvents(base, next);
    expect(events.some((event) => event.transition_kind === "branch_spawned")).toBe(true);
  });

  it("emits node_retired when status moves to abandoned", () => {
    const next = {
      ...base,
      graph_version: 2,
      nodes: [{ ...base.nodes[0], status: "abandoned" }],
    } as unknown as TypedGraphResponse;

    const events = buildTypedGraphMotionEvents(base, next);
    expect(events.some((event) => event.transition_kind === "node_retired")).toBe(true);
  });

  it("returns nothing when graph_version is unchanged", () => {
    expect(buildTypedGraphMotionEvents(base, base)).toEqual([]);
  });
});
