// Pure derivation of an agent's user-facing presence from raw server data.
// The back-end stores facts (which tasks exist, their statuses, the runtime
// last_seen_at); the front-end translates them into two orthogonal
// dimensions:
//
//   1. AgentAvailability — derived from runtime reachability only.
//   2. Workload          — derived from the task counts only.
//
// They are computed independently and assembled into AgentPresenceDetail.
// Workload is strictly "what's on the plate right now" — no historical
// terminal state. Past failures / completions live on the detail page
// (Recent Work, failure_reason) and Inbox.

import { deriveRuntimeHealth } from "../runtimes/derive-health";
import type { Agent, AgentRuntime, AgentTask } from "../types";
import type {
  AgentAvailability,
  AgentPresenceDetail,
  Workload,
} from "./types";

/** Presence-safe runtime fields. Enough for online/offline without full runtime details. */
export type RuntimeReachability = Pick<AgentRuntime, "status" | "last_seen_at">;

// AgentAvailability mirrors RuntimeHealth's reachability buckets but folds
// `about_to_gc` into `offline` — both mean "long unreachable" from the
// user's standpoint; the GC-warning copy belongs to the runtime card, not
// the agent dot.
export function deriveAgentAvailability(
  runtime: RuntimeReachability | null,
  now: number,
): AgentAvailability {
  if (!runtime) return "offline";
  const health = deriveRuntimeHealth(runtime, now);
  if (health === "online") return "online";
  if (health === "recently_lost") return "unstable";
  return "offline"; // offline | about_to_gc collapse here
}

// Atomic workload derivation: pure 3-way classification of running/queued
// counts. Exported so Runtime-level views (which already aggregate counts
// per-runtime in their own indices) can plug into the same vocabulary
// without re-deriving from raw task arrays.
export function deriveWorkload(counts: {
  runningCount: number;
  queuedCount: number;
}): Workload {
  if (counts.runningCount > 0) return "working";
  if (counts.queuedCount > 0) return "queued";
  return "idle";
}

interface WorkloadDetail {
  workload: Workload;
  runningCount: number;
  queuedCount: number;
}

// Aggregates a task list into running/queued counts, then classifies via
// deriveWorkload. Caller pre-filters to the relevant scope (per-agent or
// per-runtime) — we don't filter again here.
export function deriveWorkloadDetail(tasks: readonly AgentTask[]): WorkloadDetail {
  let runningCount = 0;
  let queuedCount = 0;
  for (const t of tasks) {
    if (t.status === "running") {
      runningCount += 1;
    } else if (
      t.status === "queued" ||
      t.status === "dispatched" ||
      // The daemon parked this task on a busy local_directory path. It's
      // still on the agent's plate (counts toward "queued" presence), but
      // it hasn't reached the run phase yet.
      t.status === "waiting_local_directory"
    ) {
      queuedCount += 1;
    }
    // Terminal statuses (completed / failed / cancelled) intentionally
    // ignored — workload is "what's on the plate right now", not history.
  }
  return {
    workload: deriveWorkload({ runningCount, queuedCount }),
    runningCount,
    queuedCount,
  };
}

interface DerivePresenceInput {
  agent: Agent;
  runtime: RuntimeReachability | null;
  // Tasks for THIS agent only. Callers (buildPresenceMap, hooks) pre-filter
  // by agent_id — we don't re-check here.
  tasks: readonly AgentTask[];
  // Wall-clock millis used by deriveAgentAvailability to bucket runtime
  // health. Threading it as a parameter keeps the function pure.
  now: number;
}

export function deriveAgentPresenceDetail(input: DerivePresenceInput): AgentPresenceDetail {
  // Archived wins over every runtime/task signal — a retired agent must
  // never read as live anywhere. Short-circuit before deriving runtime
  // health or workload so a leftover online runtime row or a stale snapshot
  // task can't leak "Online" / "Working" into any consumer.
  if (input.agent.archived_at) {
    return {
      availability: "archived",
      workload: "idle",
      runningCount: 0,
      queuedCount: 0,
      capacity: input.agent.max_concurrent_tasks,
    };
  }

  // Availability comes from runtime reachability (heartbeat). LRM-248 AC5:
  // when a private runtime is filtered from the list, callers must still pass
  // a reachability stub (status + last_seen) so "can't see runtime details"
  // does not collapse to offline.
  let availability = deriveAgentAvailability(input.runtime, input.now);
  const detail = deriveWorkloadDetail(input.tasks);

  // LRM-248: unstable/reconnecting/running → online for live chrome. An
  // in-flight running task proves the agent is reachable even when heartbeat
  // lags or the runtime row is missing from the visible list.
  if (
    detail.runningCount > 0 &&
    (availability === "offline" || availability === "unstable")
  ) {
    availability = "online";
  }

  return {
    availability,
    workload: detail.workload,
    runningCount: detail.runningCount,
    queuedCount: detail.queuedCount,
    capacity: input.agent.max_concurrent_tasks,
  };
}


/**
 * LRM-248 AC5: when ListVisibleAgentRuntimes hides another member's private
 * runtime, the agent list still projects status + last_seen so live chrome
 * can render. Prefer a full runtime row when present; otherwise use this stub.
 */
export function runtimeReachabilityFromAgent(agent: Agent): RuntimeReachability | null {
  const status = agent.runtime_status;
  if (status !== "online" && status !== "offline") return null;
  return {
    status,
    last_seen_at: agent.runtime_last_seen_at ?? null,
  };
}

// Workspace-level batch builder. One pass over the workspace's agents
// produces a Map<agentId, AgentPresenceDetail> that every list / card /
// runtime sub-page can read without re-deriving.
export function buildPresenceMap(args: {
  agents: readonly Agent[];
  runtimes: readonly AgentRuntime[];
  // The workspace agent task snapshot: every active task plus each agent's
  // most recent terminal task. Comes straight from getAgentTaskSnapshot()
  // — no pre-filtering needed. Terminal rows are silently ignored by
  // deriveWorkloadDetail (workload is current-state only).
  snapshot: readonly AgentTask[];
  now: number;
}): Map<string, AgentPresenceDetail> {
  const out = new Map<string, AgentPresenceDetail>();
  const runtimesById = new Map<string, AgentRuntime>();
  for (const r of args.runtimes) runtimesById.set(r.id, r);

  // Group tasks by agent_id once — O(N) — so per-agent derivation is O(1)
  // task scans rather than O(N×M).
  const tasksByAgent = new Map<string, AgentTask[]>();
  for (const t of args.snapshot) {
    const list = tasksByAgent.get(t.agent_id);
    if (list) list.push(t);
    else tasksByAgent.set(t.agent_id, [t]);
  }

  for (const agent of args.agents) {
    const runtime =
      runtimesById.get(agent.runtime_id) ??
      runtimeReachabilityFromAgent(agent);
    const tasks = tasksByAgent.get(agent.id) ?? [];
    out.set(agent.id, deriveAgentPresenceDetail({ agent, runtime, tasks, now: args.now }));
  }
  return out;
}
