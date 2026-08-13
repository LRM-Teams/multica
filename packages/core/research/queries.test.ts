import { describe, expect, it } from "vitest";
import { normalizeResearchPresenceMap } from "./queries";

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
