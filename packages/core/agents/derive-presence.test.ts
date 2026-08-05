import { describe, expect, it } from "vitest";
import type { Agent, AgentRuntime, AgentTask } from "../types";
import {
  buildPresenceMap,
  deriveAgentAvailability,
  deriveAgentPresenceDetail,
  deriveWorkload,
  deriveWorkloadDetail,
  runtimeReachabilityFromAgent,
} from "./derive-presence";

function makeAgent(overrides: Partial<Agent> = {}): Agent {
  return {
    id: "agent-1",
    workspace_id: "ws-1",
    runtime_id: "rt-1",
    name: "test_agent",
    display_name: "Test Agent",
    description: "",
    instructions: "",
    avatar_url: null,
    runtime_mode: "local",
    runtime_config: {},
    custom_args: [],
    status: "idle",
    workspace_role: "member",
    max_concurrent_tasks: 6,
    model: "",
    owner_id: null,
    skills: [],
    created_at: "2026-04-01T00:00:00Z",
    updated_at: "2026-04-01T00:00:00Z",
    archived_at: null,
    archived_by: null,
    ...overrides,
  };
}

function makeRuntime(overrides: Partial<AgentRuntime> = {}): AgentRuntime {
  return {
    id: "rt-1",
    workspace_id: "ws-1",
    daemon_id: "daemon-1",
    name: "Test Runtime",
    runtime_mode: "local",
    provider: "claude",
    launch_header: "",
    status: "online",
    device_info: "",
    metadata: {},
    current_version: null,
    update_state: "idle",
    runtime_health: "ok",
    owner_id: null,
    last_seen_at: "2026-04-27T11:59:50Z",
    created_at: "2026-04-01T00:00:00Z",
    updated_at: "2026-04-01T00:00:00Z",
    ...overrides,
  };
}

// Anchor for all wall-clock comparisons in the suite. Pairs with the
// runtime fixture's last_seen_at (10s before NOW) so an "online" runtime
// looks fresh by default.
const NOW = new Date("2026-04-27T12:00:00Z").getTime();

function makeTask(overrides: Partial<AgentTask> = {}): AgentTask {
  return {
    id: "task-1",
    agent_id: "agent-1",
    runtime_id: "rt-1",
    issue_id: "",
    status: "queued",
    priority: 0,
    dispatched_at: null,
    started_at: null,
    completed_at: null,
    result: null,
    error: null,
    created_at: "2026-04-27T11:00:00Z",
    ...overrides,
  };
}

describe("deriveAgentAvailability", () => {
  // Reachability dimension only — runtime + clock decide it; tasks are
  // irrelevant to this axis.

  it("returns online when runtime is fresh-online", () => {
    expect(deriveAgentAvailability(makeRuntime(), NOW)).toBe("online");
  });


  it("returns unstable ONLY for an ONLINE runtime whose heartbeat lagged (150s–5min)", () => {
    // Post-#571 unstable is reserved for a STALE ONLINE heartbeat — never an
    // explicitly-offline runtime. Online + last_seen 3.5 min ago → recently_lost
    // → unstable.
    expect(
      deriveAgentAvailability(
        makeRuntime({ status: "online", last_seen_at: "2026-04-27T11:56:30Z" }),
        NOW,
      ),
    ).toBe("unstable");
  });

  it("returns offline (NOT unstable) the moment a runtime is EXPLICITLY offline (#571)", () => {
    // #571 core regression: status="offline" + last_seen 30s ago must read
    // offline, not the old buggy "unstable" for the first 5 minutes.
    expect(
      deriveAgentAvailability(
        makeRuntime({ status: "offline", last_seen_at: "2026-04-27T11:59:30Z" }),
        NOW,
      ),
    ).toBe("offline");
  });

  it("returns offline when runtime has been gone > 5 min", () => {
    expect(
      deriveAgentAvailability(
        makeRuntime({ status: "offline", last_seen_at: "2026-04-27T11:50:00Z" }),
        NOW,
      ),
    ).toBe("offline");
  });

  it("collapses about_to_gc into offline (it's a runtime-card concern, not the dot)", () => {
    expect(
      deriveAgentAvailability(
        // 6.5 days ago — past the 6-day about_to_gc threshold.
        makeRuntime({ status: "offline", last_seen_at: "2026-04-21T00:00:00Z" }),
        NOW,
      ),
    ).toBe("offline");
  });

  it("returns offline when the runtime is null (deleted / never registered)", () => {
    expect(deriveAgentAvailability(null, NOW)).toBe("offline");
  });
});

describe("deriveWorkload", () => {
  // Atomic 3-way classifier — used by both Agent (per-agent task counts)
  // and Runtime (per-runtime aggregated counts). Pure functional mapping
  // from a count pair to a workload label.

  it("returns working when runningCount > 0", () => {
    expect(deriveWorkload({ runningCount: 1, queuedCount: 0 })).toBe("working");
    expect(deriveWorkload({ runningCount: 3, queuedCount: 5 })).toBe("working");
  });

  it("returns queued when nothing running but queuedCount > 0", () => {
    expect(deriveWorkload({ runningCount: 0, queuedCount: 1 })).toBe("queued");
    expect(deriveWorkload({ runningCount: 0, queuedCount: 5 })).toBe("queued");
  });

  it("returns idle when both counts are zero", () => {
    expect(deriveWorkload({ runningCount: 0, queuedCount: 0 })).toBe("idle");
  });
});

describe("deriveWorkloadDetail", () => {
  // Aggregates a task list into running/queued counts before classifying.
  // Terminal statuses (completed / failed / cancelled) are silently
  // ignored — workload is "what's on the plate right now", not history.

  it("returns idle when no tasks at all", () => {
    const r = deriveWorkloadDetail([]);
    expect(r.workload).toBe("idle");
    expect(r.runningCount).toBe(0);
    expect(r.queuedCount).toBe(0);
  });

  it("returns working when at least one task is running", () => {
    const r = deriveWorkloadDetail([makeTask({ status: "running" })]);
    expect(r.workload).toBe("working");
    expect(r.runningCount).toBe(1);
    expect(r.queuedCount).toBe(0);
  });

  it("returns queued when only queued / dispatched tasks exist (no running)", () => {
    // The "stuck on offline runtime" scenario in isolation: runningCount=0,
    // queuedCount>0 surfaces as `queued` so the UI can honestly say
    // "Queued · N" instead of misleading "Running 0/3 +Nq".
    const r = deriveWorkloadDetail([
      makeTask({ status: "queued" }),
      makeTask({ id: "t2", status: "dispatched" }),
    ]);
    expect(r.workload).toBe("queued");
    expect(r.runningCount).toBe(0);
    expect(r.queuedCount).toBe(2);
  });

  it("returns working when running coexists with queued (overflow)", () => {
    // Capacity-saturated agent: still running, but with a queue building.
    // The chip says "Working" with the queue expressed as a `+Nq` badge.
    const r = deriveWorkloadDetail([
      makeTask({ id: "t1", status: "running" }),
      makeTask({ id: "t2", status: "queued" }),
      makeTask({ id: "t3", status: "queued" }),
    ]);
    expect(r.workload).toBe("working");
    expect(r.runningCount).toBe(1);
    expect(r.queuedCount).toBe(2);
  });

  it("ignores terminal statuses entirely (no historical state in workload)", () => {
    // Failed / completed / cancelled tasks contribute no count and don't
    // change the verdict — Recent Work + Inbox handle history, not workload.
    const r = deriveWorkloadDetail([
      makeTask({
        id: "t-failed",
        status: "failed",
        completed_at: "2026-04-27T11:30:00Z",
      }),
      makeTask({
        id: "t-completed",
        status: "completed",
        completed_at: "2026-04-27T11:00:00Z",
      }),
      makeTask({
        id: "t-cancelled",
        status: "cancelled",
        completed_at: "2026-04-27T10:30:00Z",
      }),
    ]);
    expect(r.workload).toBe("idle");
    expect(r.runningCount).toBe(0);
    expect(r.queuedCount).toBe(0);
  });

  it("classifies running over queued when both present, regardless of order", () => {
    const r = deriveWorkloadDetail([
      makeTask({ id: "t1", status: "queued" }),
      makeTask({ id: "t2", status: "running" }),
    ]);
    expect(r.workload).toBe("working");
  });
});

describe("deriveAgentPresenceDetail", () => {
  // Composition: the two dimensions are derived independently and the
  // detail object exposes both. No cross-axis override — workload never
  // colours the dot, availability never overrides workload.

  it("composes online + working for the common busy case", () => {
    const detail = deriveAgentPresenceDetail({
      agent: makeAgent(),
      runtime: makeRuntime(),
      tasks: [
        makeTask({ status: "running" }),
        makeTask({ id: "t2", status: "queued" }),
      ],
      now: NOW,
    });
    expect(detail.availability).toBe("online");
    expect(detail.workload).toBe("working");
    expect(detail.runningCount).toBe(1);
    expect(detail.queuedCount).toBe(1);
    expect(detail.capacity).toBe(6);
  });

  it("provider quota lock folds Online→Offline even with a fresh heartbeat (#64/#77)", () => {
    const detail = deriveAgentPresenceDetail({
      agent: makeAgent({
        provider_block_detail: "429 code 1310 usage cap",
        provider_blocked_until: "2026-04-28T12:00:00Z",
      }),
      runtime: makeRuntime(),
      tasks: [makeTask({ status: "running" })],
      now: NOW,
    });
    expect(detail.availability).toBe("offline");
    expect(detail.workload).toBe("idle");
    expect(detail.runningCount).toBe(0);
  });

  it("provider lock with unknown until (null) stays Offline — never invents a reset (#815)", () => {
    const detail = deriveAgentPresenceDetail({
      agent: makeAgent({
        provider_block_detail: "quota exceeded",
        provider_blocked_until: null,
      }),
      runtime: makeRuntime(),
      tasks: [],
      now: NOW,
    });
    expect(detail.availability).toBe("offline");
  });

  it("composes offline + queued — the canonical 'stuck' case (was previously misleading 'running 0/N')", () => {
    // The motivation for the redesign: runtime offline + queued tasks
    // used to surface as `running` with `0/3 +2q` counts (literally false).
    // Workload now returns `queued` honestly, paired with offline
    // availability — UI reads "Offline · Queued · 2".
    const detail = deriveAgentPresenceDetail({
      agent: makeAgent(),
      runtime: makeRuntime({
        status: "offline",
        last_seen_at: "2026-04-27T11:50:00Z",
      }),
      tasks: [
        makeTask({ status: "queued" }),
        makeTask({ id: "t2", status: "queued" }),
      ],
      now: NOW,
    });
    expect(detail.availability).toBe("offline");
    expect(detail.workload).toBe("queued");
    expect(detail.runningCount).toBe(0);
    expect(detail.queuedCount).toBe(2);
  });

  it("composes online + working when heartbeat is unstable but a task is running (LRM-248)", () => {
    const detail = deriveAgentPresenceDetail({
      agent: makeAgent(),
      runtime: makeRuntime({
        status: "online",
        last_seen_at: "2026-04-27T11:56:00Z",
      }),
      tasks: [makeTask({ status: "running" })],
      now: NOW,
    });
    expect(detail.availability).toBe("online");
    expect(detail.workload).toBe("working");
  });

  it("composes online + working for an EXPLICITLY offline runtime with a running task (LRM-248 running→在线)", () => {
    const detail = deriveAgentPresenceDetail({
      agent: makeAgent(),
      runtime: makeRuntime({
        status: "offline",
        last_seen_at: "2026-04-27T11:59:30Z",
      }),
      tasks: [makeTask({ status: "running" })],
      now: NOW,
    });
    expect(detail.availability).toBe("online");
    expect(detail.workload).toBe("working");
  });

  it("composes offline + idle for an unreachable agent with no tasks pending", () => {
    const detail = deriveAgentPresenceDetail({
      agent: makeAgent(),
      runtime: makeRuntime({
        status: "offline",
        last_seen_at: "2026-04-27T11:50:00Z",
      }),
      tasks: [],
      now: NOW,
    });
    expect(detail.availability).toBe("offline");
    expect(detail.workload).toBe("idle");
  });

  it("task #106: does NOT promote a missing runtime + running task to online — no last_seen_at means no freshness evidence to trust", () => {
    // Was "promotes ... to online" pre-#106: a running task alone used to be
    // enough. That's the bug — task status is a RESULT, the heartbeat is the
    // CAUSE; with zero freshness signal (no runtime, no last_seen_at at all)
    // there is nothing to justify overriding an already-honest offline.
    const detail = deriveAgentPresenceDetail({
      agent: makeAgent(),
      runtime: null,
      tasks: [makeTask({ status: "running" })],
      now: NOW,
    });
    expect(detail.availability).toBe("offline");
    // Workload is a separate axis (task-derived, not runtime-derived) —
    // unaffected by the availability fix.
    expect(detail.workload).toBe("working");
  });

  it("task #106: a runtime stale beyond the recently-lost window is NOT promoted to online by a running task", () => {
    // The actual bug scenario: daemon died mid-task. The runtime sweeper
    // correctly marks the runtime offline within ~180s, but the task's
    // 'running' status can linger for hours (server-side wall-clock
    // backstop). Before the fix this stale running task would resurrect
    // "online" forever; now it can't once last_seen_at is genuinely old.
    const detail = deriveAgentPresenceDetail({
      agent: makeAgent(),
      runtime: makeRuntime({
        status: "offline",
        last_seen_at: "2026-04-27T11:00:00Z", // 1 hour stale
      }),
      tasks: [makeTask({ status: "running" })],
      now: NOW,
    });
    expect(detail.availability).toBe("offline");
    expect(detail.workload).toBe("working");
  });

  it("returns idle workload when only terminal tasks are present (history doesn't bleed in)", () => {
    const detail = deriveAgentPresenceDetail({
      agent: makeAgent(),
      runtime: makeRuntime(),
      tasks: [
        makeTask({
          status: "failed",
          completed_at: "2026-04-27T11:30:00Z",
        }),
      ],
      now: NOW,
    });
    expect(detail.availability).toBe("online");
    expect(detail.workload).toBe("idle");
  });

  it("mirrors agent.max_concurrent_tasks into capacity", () => {
    const detail = deriveAgentPresenceDetail({
      agent: makeAgent({ max_concurrent_tasks: 3 }),
      runtime: makeRuntime(),
      tasks: [],
      now: NOW,
    });
    expect(detail.capacity).toBe(3);
  });

  it("reports archived over any runtime/task signal for an archived agent", () => {
    // Archived wins over presence: a leftover online runtime and a running
    // task must never make a retired agent read as live. Availability
    // collapses to "archived" and workload is forced idle with zero counts
    // so no consumer (dot, hover card, list row) can surface "Online" or
    // "Working" for an archived agent.
    const detail = deriveAgentPresenceDetail({
      agent: makeAgent({ archived_at: "2026-04-27T10:00:00Z" }),
      runtime: makeRuntime(),
      tasks: [makeTask({ status: "running" })],
      now: NOW,
    });
    expect(detail.availability).toBe("archived");
    expect(detail.workload).toBe("idle");
    expect(detail.runningCount).toBe(0);
    expect(detail.queuedCount).toBe(0);
  });
});

describe("buildPresenceMap", () => {
  it("returns one entry per agent, sourcing tasks by agent_id from a flat list", () => {
    const agentA = makeAgent({ id: "a", runtime_id: "rt-1" });
    const agentB = makeAgent({ id: "b", runtime_id: "rt-1" });
    const map = buildPresenceMap({
      agents: [agentA, agentB],
      runtimes: [makeRuntime()],
      snapshot: [
        makeTask({ id: "t1", agent_id: "a", status: "running" }),
        makeTask({ id: "t2", agent_id: "b", status: "queued" }),
      ],
      now: NOW,
    });
    const a = map.get("a");
    const b = map.get("b");
    expect(a?.availability).toBe("online");
    expect(a?.workload).toBe("working");
    expect(b?.availability).toBe("online");
    expect(b?.workload).toBe("queued");
  });

  it("task #106: agents whose runtime_id has no matching runtime and no freshness evidence stay offline, even with a running task", () => {
    const orphan = makeAgent({ id: "orphan", runtime_id: "missing" });
    const map = buildPresenceMap({
      agents: [orphan],
      runtimes: [],
      snapshot: [makeTask({ agent_id: "orphan", status: "running" })],
      now: NOW,
    });
    const o = map.get("orphan");
    expect(o?.availability).toBe("offline");
    // Workload still resolves independently — running task counts.
    expect(o?.workload).toBe("working");
  });

  it("threads the same `now` so every agent on a shared runtime gets the same availability", () => {
    // Multi-agent scenario: one local daemon backs N agents, its heartbeat
    // lags (still ONLINE, last_seen 4 min ago → recently_lost). All dependent
    // agents should report unstable together — the shared `now` parameter is
    // what guarantees consistent bucket boundaries.
    const agentA = makeAgent({ id: "a", runtime_id: "rt-1" });
    const agentB = makeAgent({ id: "b", runtime_id: "rt-1" });
    const map = buildPresenceMap({
      agents: [agentA, agentB],
      runtimes: [
        makeRuntime({
          status: "online",
          last_seen_at: "2026-04-27T11:56:00Z",
        }),
      ],
      snapshot: [
        makeTask({ id: "t1", agent_id: "a", status: "queued" }),
        makeTask({ id: "t2", agent_id: "b", status: "running" }),
      ],
      now: NOW,
    });
    expect(map.get("a")?.availability).toBe("unstable");
    expect(map.get("b")?.availability).toBe("online");
    // Workload remains independent: a is queued (waiting), b is working.
    expect(map.get("a")?.workload).toBe("queued");
    expect(map.get("b")?.workload).toBe("working");
  });

  it("ignores terminal tasks in the snapshot when building per-agent workload", () => {
    // Snapshot intentionally still includes each agent's most recent
    // terminal task (back-end SQL didn't change); the front-end now
    // filters them out at the workload-derivation step.
    const agentA = makeAgent({ id: "a", runtime_id: "rt-1" });
    const map = buildPresenceMap({
      agents: [agentA],
      runtimes: [makeRuntime()],
      snapshot: [
        makeTask({
          id: "t-terminal",
          agent_id: "a",
          status: "failed",
          completed_at: "2026-04-27T11:30:00Z",
        }),
      ],
      now: NOW,
    });
    expect(map.get("a")?.workload).toBe("idle");
  });
});

describe("runtimeReachabilityFromAgent (LRM-248 AC5)", () => {
  it("projects online/offline from agent fields when the runtime list hides private details", () => {
    const agent = makeAgent({
      runtime_status: "online",
      runtime_last_seen_at: new Date(NOW - 10_000).toISOString(),
    });
    const stub = runtimeReachabilityFromAgent(agent);
    expect(stub).toEqual({
      status: "online",
      last_seen_at: agent.runtime_last_seen_at,
    });
    expect(deriveAgentAvailability(stub, NOW)).toBe("online");
  });

  it("buildPresenceMap uses the agent stub when the runtime is absent from the list", () => {
    const agent = makeAgent({
      runtime_id: "private-rt",
      runtime_status: "online",
      runtime_last_seen_at: new Date(NOW - 10_000).toISOString(),
    });
    const map = buildPresenceMap({
      agents: [agent],
      runtimes: [], // simulates a stale or partial runtime-list response
      snapshot: [],
      now: NOW,
    });
    expect(map.get(agent.id)?.availability).toBe("online");
  });

  it("task #106: a running task WITHOUT any runtime projection stays offline — no freshness evidence available", () => {
    expect(runtimeReachabilityFromAgent(makeAgent())).toBeNull();
    const map = buildPresenceMap({
      agents: [makeAgent({ runtime_id: "missing" })],
      runtimes: [],
      snapshot: [makeTask({ status: "running" })],
      now: NOW,
    });
    expect(map.get("agent-1")?.availability).toBe("offline");
    expect(map.get("agent-1")?.workload).toBe("working");
  });
});
