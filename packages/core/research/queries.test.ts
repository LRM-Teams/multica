import { describe, expect, it, vi } from "vitest";
import type { TypedGraphResponse } from "./graph-typed";
import {
  normalizeResearchPresenceMap,
  requireConsistentTypedGraphPages,
} from "./queries";

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
    vi.spyOn(Date, "now").mockReturnValueOnce(42);
    expect(normalizeResearchPresenceMap({ worker: { phase: "future" } }).worker).toEqual(
      expect.objectContaining({ phase: "idle", activity: "", updatedAt: 42, nodeId: null }),
    );
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
