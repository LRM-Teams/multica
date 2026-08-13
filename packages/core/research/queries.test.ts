import { describe, expect, it, vi } from "vitest";
import type { TypedGraphResponse } from "./graph-typed";
import {
  nextTypedGraphPageOffset,
  normalizeResearchPresenceMap,
  requireConsistentTypedGraphPages,
} from "./queries";

function graphPage(
  count: number,
  totalNodeCount: number | null,
): TypedGraphResponse {
  return {
    session_id: "s1",
    graph_version: 1,
    total_node_count: totalNodeCount,
    nodes: Array.from({ length: count }, (_, index) => ({
      id: `n-${index}`,
    })) as TypedGraphResponse["nodes"],
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
  };
}

describe("normalizeResearchPresenceMap", () => {
  it("keeps the full v2 roster including idle workers and location fields", () => {
    const normalized = normalizeResearchPresenceMap({
      idle: { activity: "", phase: "idle", updated_at: 10 },
      running: {
        activity: "Checking sources",
        phase: "running",
        updated_at: 20,
        role: "scout",
        fleet_member_id: "fm1",
        task_id: "task1",
        node_id: "node1",
        branch_id: "branch1",
        stage: "s2_sources",
        expires_at: 30,
      },
    });
    expect(Object.keys(normalized)).toEqual(["idle", "running"]);
    expect(normalized.running).toEqual(expect.objectContaining({
      phase: "running",
      nodeId: "node1",
      taskId: "task1",
      stage: "s2_sources",
    }));
  });

  it("downgrades unknown phases and malformed optional fields safely", () => {
    expect(normalizeResearchPresenceMap({ worker: { phase: "future" } }).worker).toEqual(
      expect.objectContaining({
        phase: "unknown",
        activity: "",
        updatedAt: null,
        nodeId: null,
      }),
    );
  });

  it("does not manufacture a fresh timestamp for an undated presence entry", () => {
    expect(
      normalizeResearchPresenceMap({ worker: { phase: "running" } }).worker,
    ).toEqual(expect.objectContaining({ phase: "running", updatedAt: null }));
  });
});
describe("nextTypedGraphPageOffset", () => {
  it("uses the canonical total when the server provides it", () => {
    const first = graphPage(500, 750);
    expect(nextTypedGraphPageOffset(first, [first])).toBe(500);

    const second = graphPage(250, 750);
    expect(nextTypedGraphPageOffset(second, [first, second])).toBeUndefined();
  });

  it("continues after a full compatibility page without a total", () => {
    const full = graphPage(500, null);
    expect(nextTypedGraphPageOffset(full, [full])).toBe(500);
  });

  it("stops after a short compatibility page without a total", () => {
    const full = graphPage(500, null);
    const short = graphPage(37, null);
    expect(nextTypedGraphPageOffset(short, [full, short])).toBeUndefined();
  });

  it("retains a known total when a later page omits it", () => {
    const first = graphPage(500, 1200);
    const second = graphPage(500, null);
    expect(nextTypedGraphPageOffset(second, [first, second])).toBe(1000);
  });

  it("uses the latest known total when the graph shrinks", () => {
    const first = graphPage(500, 1200);
    const second = graphPage(300, 800);
    expect(nextTypedGraphPageOffset(second, [first, second])).toBeUndefined();
  });
});

describe("requireConsistentTypedGraphPages", () => {
  const page = (version: number) =>
    ({ graph_version: version }) as TypedGraphResponse;

  it("preserves pages from one canonical graph version", () => {
    const data = {
      pages: [page(7), page(7)],
      pageParams: [0, 500],
    };
    expect(requireConsistentTypedGraphPages(data)).toBe(data);
  });

  it("rejects offset pages from mixed graph versions", () => {
    expect(() =>
      requireConsistentTypedGraphPages({
        pages: [page(7), page(8)],
        pageParams: [0, 500],
      }),
    ).toThrow("returned mixed graph versions");
  });
});
