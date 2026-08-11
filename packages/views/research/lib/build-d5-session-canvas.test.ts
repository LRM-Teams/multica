import { describe, expect, it } from "vitest";
import {
  TypedGraphResponseSchema,
  type TypedGraphResponse,
} from "@multica/core/research";
import { buildD5SessionCanvasModel } from "./build-d5-session-canvas";
import { testTypedNode } from "./test-typed-graph-node";
import { extractLayoutResultFromViewModel } from "../star-graph/lib/star-canvas-view-model";

const emptyLineage = {
  derived: {},
  merged: {},
  superseded: {},
  restarted: {},
  invalidated: {},
  supersedes: {},
};

function fixture(extraNodes: Record<string, unknown>[] = []): TypedGraphResponse {
  return TypedGraphResponseSchema.parse({
    session_id: "s1",
    graph_version: 1,
    nodes: [
      {
        id: "goal",
        level: "xxl",
        node_type: "goal",
        title: "Goal",
        status: "active",
        cluster_id: null,
        parent_id: null,
      },
      {
        id: "stable",
        level: "l",
        node_type: "finding",
        title: "Stable",
        status: "done",
        cluster_id: "cluster-a",
        parent_id: "goal",
      },
      ...extraNodes,
    ],
    edges: [{ id: "e1", from_node_id: "goal", to_node_id: "stable", edge_type: "leads_to" }],
    clusters: [{ id: "cluster-a", label: "Theme A", cluster_type: "topic" }],
    lineage: emptyLineage,
  });
}

describe("buildD5SessionCanvasModel", () => {
  const viewport = { width: 1200, height: 800 };

  it("stores pre-rebase layout for incremental reuse", () => {
    const first = buildD5SessionCanvasModel(fixture(), viewport, { rightPanelWidth: 0 })!;
    const second = buildD5SessionCanvasModel(
      fixture([
        testTypedNode({
          id: "probe",
          level: "s",
          node_type: "probe",
          title: "Probe",
          status: "running",
          parent_id: "stable",
        }),
      ]),
      viewport,
      { rightPanelWidth: 0, previousLayout: first.layoutForNext },
    )!;

    const stableBefore = first.layoutForNext.nodes.find((node) => node.id === "stable");
    const stableAfter = second.layoutForNext.nodes.find((node) => node.id === "stable");
    expect(stableBefore).toBeTruthy();
    expect(stableAfter?.x).toBe(stableBefore!.x);
    expect(stableAfter?.y).toBe(stableBefore!.y);
    expect(second.model.stats.reused).toBeGreaterThan(0);
  });

  it("regression: rebased positions must not feed incremental layout", () => {
    const first = buildD5SessionCanvasModel(fixture(), viewport, { rightPanelWidth: 360 })!;
    const rebasedPrevious = extractLayoutResultFromViewModel(first.model);
    const second = buildD5SessionCanvasModel(fixture(), viewport, {
      rightPanelWidth: 360,
      previousLayout: rebasedPrevious,
    })!;

    const stableFirst = first.layoutForNext.nodes.find((node) => node.id === "stable");
    const stableSecond = second.layoutForNext.nodes.find((node) => node.id === "stable");
    expect(stableFirst?.x).not.toBe(stableSecond?.x);
  });
});
