import { describe, expect, it } from "vitest";
import type { TypedGraphResponse } from "@multica/core/research";
import {
  buildTypedGraphMotionEvents,
  shouldSkipTypedGraphMotionCatchUp,
} from "./build-typed-graph-motion-events";

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

  it("emits goal_modified when new goal_version_id tags appear", () => {
    const next = {
      ...base,
      graph_version: 2,
      nodes: [{ ...base.nodes[0], goal_version_id: "gv2" }],
    } as unknown as TypedGraphResponse;

    const events = buildTypedGraphMotionEvents(base, next);
    expect(events.some((event) => event.transition_kind === "goal_modified")).toBe(true);
  });
});

describe("shouldSkipTypedGraphMotionCatchUp", () => {
  it("skips when graph_version jumps by more than one", () => {
    const previous = { ...base, graph_version: 1 } as TypedGraphResponse;
    const next = {
      ...base,
      graph_version: 4,
      nodes: [...base.nodes, { id: "b", title: "B", status: "running", level: "s" }],
    } as unknown as TypedGraphResponse;
    const events = buildTypedGraphMotionEvents(previous, next);
    expect(shouldSkipTypedGraphMotionCatchUp(previous, next, events)).toBe(true);
  });

  it("skips when many nodes appear at once (resync catch-up)", () => {
    const previous = { ...base, graph_version: 1, nodes: [] } as unknown as TypedGraphResponse;
    const next = {
      ...base,
      graph_version: 2,
      nodes: Array.from({ length: 8 }, (_, i) => ({
        id: `n${i}`,
        title: `N${i}`,
        status: "running",
        level: "s",
      })),
    } as unknown as TypedGraphResponse;
    const events = buildTypedGraphMotionEvents(previous, next);
    expect(events.length).toBeGreaterThan(6);
    expect(shouldSkipTypedGraphMotionCatchUp(previous, next, events)).toBe(true);
  });

  it("allows a small live delta batch", () => {
    const next = {
      ...base,
      graph_version: 2,
      nodes: [...base.nodes, { id: "b", title: "B", status: "running", level: "s" }],
    } as unknown as TypedGraphResponse;
    const events = buildTypedGraphMotionEvents(base, next);
    expect(shouldSkipTypedGraphMotionCatchUp(base, next, events)).toBe(false);
  });
});
