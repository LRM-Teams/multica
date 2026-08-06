import { describe, expect, it } from "vitest";
import type { ResearchPresenceMap } from "@multica/core/research";
import type {
  ResearchFleetMember,
  ResearchGraphNode,
  ResearchRunSnapshot,
} from "@multica/core/types";
import { buildExecutionOverlayRows } from "./execution-adapter";

const members = ["lead", "scout", "domain", "reviewer", "reporter"].map(
  (role, index) => ({
    id: `member-${index}`,
    agent_id: `agent-${index}`,
    role,
    status: "active",
    is_lead: index === 0,
    display_name: `Agent ${index + 1}`,
  }),
) satisfies ResearchFleetMember[];

function signal(
  overrides: Partial<ResearchPresenceMap[string]>,
): ResearchPresenceMap[string] {
  return {
    activity: "",
    updatedAt: 1,
    phase: "idle",
    role: "",
    fleetMemberId: null,
    taskId: null,
    nodeId: null,
    branchId: null,
    stage: null,
    expiresAt: null,
    staleReason: null,
    ...overrides,
  };
}

const node = {
  id: "node-running",
  session_id: "session",
  node_type: "probe",
  title: "Pricing evidence",
  summary: "",
  status: "active",
  actor_agent_id: "agent-1",
  payload: {},
  created_at: "",
  updated_at: "",
} satisfies ResearchGraphNode;

/** Minimal run snapshot — the adapter only reads attempts/tasks/sources/claims. */
function run(attempts: unknown[], claims: unknown[] = [], sources: unknown[] = []): ResearchRunSnapshot {
  return {
    run: {},
    contract: {},
    questions: [],
    tasks: [],
    attempts,
    sources,
    observations: [],
    claims,
    gate: { passed: true, findings: [] },
  } as unknown as ResearchRunSnapshot;
}

const NOW = 1_700_000_000_000;

describe("buildExecutionOverlayRows — state derivation (contract PR #2415)", () => {
  it("derives states strictly from the projection", () => {
    const rows = buildExecutionOverlayRows({
      members,
      presence: {
        "agent-0": signal({ phase: "running" }), // running (unexpired)
        "agent-1": signal({ phase: "running", expiresAt: NOW - 1 }), // expired → stale
        "agent-2": signal({ phase: "queued" }), // queued (assigned, not started)
        "agent-3": signal({ phase: "done" }), // done
        // agent-4 presence failed + newer in-flight attempt → retrying
        "agent-4": signal({ phase: "failed" }),
      },
      nodes: [node],
      run: run(
        [
          {
            id: "a1",
            task_id: "t1",
            attempt_number: 1,
            assigned_agent_id: "agent-4",
            status: "failed",
            started_at: new Date(NOW - 200_000).toISOString(),
          },
          {
            id: "a2",
            task_id: "t1",
            attempt_number: 2,
            assigned_agent_id: "agent-4",
            status: "running",
            started_at: new Date(NOW - 60_000).toISOString(),
          },
        ],
        [
          {
            id: "c1",
            produced_by_task_id: "t1",
            text: "Enterprise pricing caps under renewal",
            status: "accepted",
            created_at: new Date(NOW - 30_000).toISOString(),
          },
        ],
      ),
      now: NOW,
    });

    const byId = Object.fromEntries(rows.map((r) => [r.id, r]));
    expect(byId["agent-0"]!.status).toBe("running");
    expect(byId["agent-1"]!.status).toBe("stale");
    expect(byId["agent-2"]!.status).toBe("queued");
    expect(byId["agent-3"]!.status).toBe("done");
    expect(byId["agent-4"]!.status).toBe("retrying");
  });

  it("maps presence idle to idle and an in-flight cancellation to cancelling", () => {
    const rows = buildExecutionOverlayRows({
      members,
      presence: {
        "agent-0": signal({ phase: "idle" }), // roster present, no running evidence → idle
        "agent-1": signal({ phase: "running" }), // attempt ledger shows cancelling → cancelling
      },
      nodes: [],
      run: run([
        {
          id: "c1",
          task_id: "t1",
          attempt_number: 1,
          assigned_agent_id: "agent-1",
          status: "cancelling",
        },
      ]),
      now: NOW,
    });
    const byId = Object.fromEntries(rows.map((r) => [r.id, r]));
    expect(byId["agent-0"]!.status).toBe("idle");
    expect(byId["agent-1"]!.status).toBe("cancelling");
    expect(rows.some((r) => r.status === "running")).toBe(false);
  });

  it("missing presence is offline (never idle), distinct from stale and running", () => {
    const rows = buildExecutionOverlayRows({
      members,
      presence: {}, // no entries at all
      nodes: [],
      now: NOW,
    });
    expect(rows).toHaveLength(members.length);
    for (const row of rows) expect(row.status).toBe("offline");

    // A full roster without any running signal must not render running.
    expect(rows.some((r) => r.status === "running")).toBe(false);
  });

  it("maps failed without a retry to failed, and unknown phase to unknown", () => {
    const rows = buildExecutionOverlayRows({
      members,
      presence: {
        "agent-0": signal({ phase: "failed" }),
        "agent-1": signal({ phase: "mystery-phase" as ResearchPresenceMap[string]["phase"] }),
      },
      nodes: [],
      run: run([
        {
          id: "a1",
          task_id: "t1",
          attempt_number: 1,
          assigned_agent_id: "agent-0",
          status: "failed",
          started_at: new Date(NOW - 200_000).toISOString(),
        },
      ]),
      now: NOW,
    });
    const byId = Object.fromEntries(rows.map((r) => [r.id, r]));
    expect(byId["agent-0"]!.status).toBe("failed"); // no in-flight retry
    expect(byId["agent-1"]!.status).toBe("unknown");
  });

  it("surfaces task objective, start time, elapsed, update time and most recent accepted result", () => {
    const rows = buildExecutionOverlayRows({
      members,
      presence: {
        "agent-1": signal({ phase: "running", updatedAt: NOW - 10_000, nodeId: node.id, taskId: "t1" }),
      },
      nodes: [node],
      run: (() => {
        const r = run(
          [
            {
              id: "a1",
              task_id: "t1",
              attempt_number: 1,
              assigned_agent_id: "agent-1",
              status: "running",
              started_at: new Date(NOW - 120_000).toISOString(),
            },
          ],
          [
            {
              id: "c1",
              produced_by_task_id: "t1",
              text: "Latest accepted claim",
              created_at: new Date(NOW - 40_000).toISOString(),
            },
            {
              id: "c0",
              produced_by_task_id: "t1",
              text: "Older accepted claim",
              created_at: new Date(NOW - 400_000).toISOString(),
            },
          ],
        );
        r.tasks = [
          { id: "t1", objective: "Verify supplier regional terms", expected_result: undefined } as ResearchRunSnapshot["tasks"][number],
        ];
        return r;
      })(),
      now: NOW,
    });
    const scout = rows.find((r) => r.id === "agent-1")!;
    expect(scout.status).toBe("running");
    expect(scout.taskObjective).toBe("Verify supplier regional terms");
    expect(scout.startedAt).toBe(NOW - 120_000);
    expect(scout.updatedAt).toBe(NOW - 10_000);
    expect(scout.elapsedMs).toBe(120_000);
    // Most recent accepted result wins.
    expect(scout.recentResult).toEqual({
      id: "c1",
      title: "Latest accepted claim",
      acceptedAt: NOW - 40_000,
    });
    expect(scout.currentNodeId).toBe(node.id);
    expect(scout.locationLabel).toBe("Pricing evidence");
  });

  it("binds the locate node from presence.nodeId when resolvable", () => {
    const rows = buildExecutionOverlayRows({
      members,
      presence: { "agent-1": signal({ phase: "running", nodeId: node.id }) },
      nodes: [node],
      now: NOW,
    });
    expect(rows.find((r) => r.id === "agent-1")!.currentNodeId).toBe(node.id);
  });
});
