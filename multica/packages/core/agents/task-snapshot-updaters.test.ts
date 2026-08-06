import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import type { AgentTask } from "../types";
import { agentTaskSnapshotKeys } from "./queries";
import { patchAgentTaskSnapshotStatus } from "./task-snapshot-updaters";

const WS = "ws1";

function makeTask(overrides: Partial<AgentTask> & { id: string }): AgentTask {
  return {
    agent_id: "a1",
    runtime_id: "r1",
    issue_id: "",
    status: "queued",
    priority: 0,
    dispatched_at: null,
    started_at: null,
    completed_at: null,
    result: null,
    error: null,
    created_at: "2026-07-28T00:00:00Z",
    ...overrides,
  };
}

function seed(rows: AgentTask[]): QueryClient {
  const qc = new QueryClient();
  qc.setQueryData(agentTaskSnapshotKeys.list(WS), rows);
  return qc;
}

function snapshot(qc: QueryClient): AgentTask[] | undefined {
  return qc.getQueryData(agentTaskSnapshotKeys.list(WS));
}

describe("patchAgentTaskSnapshotStatus", () => {
  it("patches an existing row's status in place and reports handled", () => {
    const qc = seed([makeTask({ id: "t1", status: "queued" })]);

    const handled = patchAgentTaskSnapshotStatus(qc, WS, "t1", "running");

    expect(handled).toBe(true);
    expect(snapshot(qc)![0]!.status).toBe("running");
  });

  it("keeps other rows and their identity untouched", () => {
    const other = makeTask({ id: "t2", agent_id: "a2", status: "running" });
    const qc = seed([makeTask({ id: "t1", status: "queued" }), other]);

    patchAgentTaskSnapshotStatus(qc, WS, "t1", "completed");

    const rows = snapshot(qc)!;
    expect(rows[0]!.status).toBe("completed");
    expect(rows[1]).toBe(other); // referential stability → no needless re-render
  });

  it("is a no-op (still handled) when the status is unchanged", () => {
    const qc = seed([makeTask({ id: "t1", status: "running" })]);
    const before = snapshot(qc);

    const handled = patchAgentTaskSnapshotStatus(qc, WS, "t1", "running");

    expect(handled).toBe(true);
    expect(snapshot(qc)).toBe(before); // same array reference, no setQueryData
  });

  it("returns false for a brand-new non-terminal task (caller must refetch)", () => {
    const qc = seed([makeTask({ id: "t1" })]);

    const handled = patchAgentTaskSnapshotStatus(qc, WS, "new", "queued");

    expect(handled).toBe(false);
    expect(snapshot(qc)?.length).toBe(1); // not inserted
  });

  it("ignores (handled=true) a terminal event for an untracked task", () => {
    const qc = seed([makeTask({ id: "t1" })]);

    const handled = patchAgentTaskSnapshotStatus(qc, WS, "gone", "completed");

    expect(handled).toBe(true);
    expect(snapshot(qc)?.length).toBe(1);
  });

  it("handles a burst of status transitions on tracked tasks with ZERO refetch signals", () => {
    // Acceptance for step②: the old handler invalidated the whole-workspace
    // snapshot on EVERY task event (one full refetch per event, debounced).
    // With in-place patching, a burst of lifecycle transitions on tasks already
    // in the snapshot must trigger NO refetch — `patchAgentTaskSnapshotStatus`
    // returns true (handled in place) for every event.
    const qc = seed([
      makeTask({ id: "t1", agent_id: "a1", status: "queued" }),
      makeTask({ id: "t2", agent_id: "a2", status: "queued" }),
      makeTask({ id: "t3", agent_id: "a3", status: "queued" }),
    ]);
    const burst: Array<[string, AgentTask["status"]]> = [
      ["t1", "dispatched"], ["t1", "running"], ["t1", "completed"],
      ["t2", "dispatched"], ["t2", "running"], ["t2", "failed"],
      ["t3", "running"], ["t3", "cancelled"],
    ];

    const refetchSignals = burst.filter(
      ([id, s]) => !patchAgentTaskSnapshotStatus(qc, WS, id, s),
    ).length;

    expect(refetchSignals).toBe(0); // old behavior would have been 8 refetches
    const rows = snapshot(qc)!;
    expect(rows.find((t) => t.id === "t1")!.status).toBe("completed");
    expect(rows.find((t) => t.id === "t2")!.status).toBe("failed");
    expect(rows.find((t) => t.id === "t3")!.status).toBe("cancelled");
  });

  it("coalesces a burst of brand-new tasks into refetch signals (caller debounces to one)", () => {
    // New tasks can't be inserted from the payload (AgentTask required fields),
    // so each returns false → the hook coalesces these into ONE snapshot
    // refetch per 500ms window. Still far cheaper than the old per-event refetch.
    const qc = seed([makeTask({ id: "t1", status: "running" })]);
    const newTasks = ["n1", "n2", "n3", "n4"];

    const needRefetch = newTasks.filter(
      (id) => !patchAgentTaskSnapshotStatus(qc, WS, id, "queued"),
    ).length;

    expect(needRefetch).toBe(4); // 4 signals → debouncedRefresh collapses to 1 fetch
  });

  it("returns handled without creating the cache when the query is not active", () => {
    const qc = new QueryClient(); // nothing seeded

    const handled = patchAgentTaskSnapshotStatus(qc, WS, "t1", "running");

    expect(handled).toBe(true);
    expect(snapshot(qc)).toBeUndefined(); // did not materialize an empty cache
  });
});
