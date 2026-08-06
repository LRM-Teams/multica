import type { ResearchPresenceMap } from "@multica/core/research";
import type { ResearchFleetMember, ResearchGraphNode, ResearchRunAttempt, ResearchRunSnapshot } from "@multica/core/types";
import {
  EXECUTION_STATUS_ACTION_KEY,
  type ExecutionActionKey,
  type ExecutionStatus,
} from "./execution-status";

/**
 * LRM-1473 / LRM-1479 — Presence + run snapshot → 8-state execution rows.
 *
 * Pure field mapping over the authoritative Projection only (Presence
 * contract v2 + `ResearchRunSnapshot` task/attempt/result data). It never
 * infers state from chat, animations or activity captions. Display grouping is
 * a front-end concern and is never written back to the Projection / Insight.
 *
 * Key rules (LRM-1473 §3):
 *  - a missing (non-archived) roster key → `offline`, NOT `idle` — presence
 *    absence means the member is not at their post;
 *  - a `running` / `queued` signal past `expires_at` → `stale` (contract v2);
 *  - `failed` + a newer in-flight attempt for the same agent → `retrying`;
 *  - `idle` with an active presence entry → `waiting` (roster present, no
 *    running evidence; never fake `running`).
 */

export type ExecutionRecentResult = {
  /** Stable result id (task-produced claim / source / observation). */
  id: string;
  /** Human title / text of the accepted artifact. */
  title: string;
  /** Unix ms when the artifact was accepted / submitted. */
  acceptedAt: number;
};

export type ExecutionRow = {
  id: string;
  name: string;
  role: string;
  initials: string;
  avatarUrl?: string;
  status: ExecutionStatus;
  /** Live server activity text (locale-appropriate); undefined when none. */
  action?: string;
  /** Fallback semantic action key when no live text is present. */
  actionKey: ExecutionActionKey;
  /** Expanded detail text (e.g. stale/offline/waiting reason). */
  actionDetail?: string;
  /** Failure / wait reason string when the projection provides one. */
  reason?: string;
  /** Failure reason: only when status is `failed`. */
  failureReasonKey?: string;

  /** Absolute start time (unix ms) of the current work, when known. */
  startedAt?: number;
  /** Last update (unix ms). Always present from presence.updatedAt. */
  updatedAt: number;
  /** Elapsed (ms) since start; only meaningful when running/waiting/retrying. */
  elapsedMs?: number;

  /** Most recent accepted result artifact for this agent, when known. */
  recentResult?: ExecutionRecentResult;

  /** Assigned canvas task node id, when resolvable. */
  currentNodeId?: string;
  /** Human readable node label for the locate affordance. */
  locationLabel?: string;

  /** Bindings surfaced in the expanded detail (stage / task / attempt / branch). */
  taskId?: string | null;
  attemptId?: string | null;
  branchId?: string | null;
  stage?: string | null;
  staleReason?: string | null;
  waitingReason?: string | null;
};

function initials(name: string): string {
  const compact = name.trim();
  if (!compact) return "AI";
  return compact
    .split(/\s+/)
    .map((part) => part[0])
    .join("")
    .slice(0, 2)
    .toUpperCase();
}

function toUnixMs(value: number | string | null | undefined): number | undefined {
  if (value == null) return undefined;
  if (typeof value === "number") return value;
  const ms = Date.parse(value);
  return Number.isFinite(ms) ? ms : undefined;
}

type AttemptsByAgent = Map<string, ResearchRunAttempt[]>;

function attemptsByAgent(run: ResearchRunSnapshot | undefined): AttemptsByAgent {
  const map: AttemptsByAgent = new Map();
  if (!run) return map;
  for (const attempt of run.attempts) {
    if (!attempt.assigned_agent_id) continue;
    const list = map.get(attempt.assigned_agent_id);
    if (list) list.push(attempt);
    else map.set(attempt.assigned_agent_id, [attempt]);
  }
  for (const list of map.values()) {
    list.sort((a, b) => (a.attempt_number ?? 0) - (b.attempt_number ?? 0));
  }
  return map;
}

/** True when the lease for a running/queued signal is not yet expired. */
function isLeaseValid(entry: ResearchPresenceMap[string]): boolean {
  if (entry.expiresAt == null) return true;
  return Date.now() < entry.expiresAt;
}

function isRetrying(
  presence: ResearchPresenceMap[string] | undefined,
  attempts: ResearchRunAttempt[] | undefined,
): boolean {
  // 1. Explicit retry phase on the presence entry.
  if (presence?.phase && isRunningLike(presence.phase)) {
    const failedAttempt = attempts?.slice().reverse().find((a) => isTerminalFailure(a.status));
    if (failedAttempt && toUnixMs(attempts?.at(-1)?.started_at)) return true;
  }
  // 2. A newer in-flight attempt after a failed terminal attempt for the agent.
  if (attempts && attempts.length > 0) {
    const lastIndex = attempts.length - 1;
    const last = attempts[lastIndex]!;
    const inFlight = last.status === "dispatching" || last.status === "running";
    if (inFlight) {
      const priorFailure = attempts
        .slice(0, lastIndex)
        .some((a) => isTerminalFailure(a.status));
      if (priorFailure) return true;
    }
  }
  return false;
}

function isRunningLike(phase: string | null | undefined): boolean {
  return phase === "running" || phase === "queued" || phase === "dispatching";
}

function isTerminalFailure(status: string | null | undefined): boolean {
  return status === "failed" || status === "lost" || status === "cancelled";
}

type PresenceLike = ResearchPresenceMap[string] | undefined;

function deriveStatus(
  presence: PresenceLike,
  attempts: ResearchRunAttempt[] | undefined,
  now: number,
): ExecutionStatus {
  if (!presence) return "offline";
  const phase = presence.phase;
  if (phase === "stale") return "stale";
  if (phase === "failed") {
    return isRetrying(presence, attempts) ? "retrying" : "failed";
  }
  if (phase === "running" || phase === "queued") {
    // Contract: queued/running past expires_at → stale.
    if (presence.expiresAt != null && now >= presence.expiresAt) return "stale";
    const active = isRunningLike(phase) && isLeaseValid(presence);
    // presence running → running; queued → waiting (roster present).
    return phase === "running" && active ? "running" : "waiting";
  }
  if (phase === "done") return "done";
  if (phase === "idle") return "waiting";
  // Unknown phase value → unknown (never guess).
  return "unknown";
}

function lastAcceptedResult(
  run: ResearchRunSnapshot | undefined,
  memberId: string,
  nodeByAgent: Map<string, ResearchGraphNode> | undefined,
): ExecutionRecentResult | undefined {
  if (!run) return undefined;
  // Collect artifacts produced by this agent's tasks: claims (the strongest
  // accepted structured artifact) then sources, ordered by acceptance time.
  const produced = new Map<string, { id: string; title: string; at: number }>();
  const agentNode = nodeByAgent?.get(memberId);
  const taskIds = new Set<string>();
  const taskIdByNode =
    agentNode?.payload && typeof agentNode.payload === "object"
      ? (agentNode.payload as Record<string, unknown>).task_id
      : undefined;
  if (typeof taskIdByNode === "string") taskIds.add(taskIdByNode);
  for (const t of run.tasks) {
    // Task → assigned agent is not a first-class field on ResearchRunTask, so
    // rely on the run attempts ledger to map agent ↔ task.
    void t;
  }
  for (const attempt of run.attempts) {
    if (attempt.assigned_agent_id === memberId) taskIds.add(attempt.task_id);
  }
  const push = (id: string, title: string, at: number | undefined) => {
    const ts = at ?? Date.now();
    const prev = produced.get(id);
    if (!prev || ts >= prev.at) produced.set(id, { id, title, at: ts });
  };
  for (const claim of run.claims ?? []) {
    if (taskIds.has(claim.produced_by_task_id ?? "")) {
      push(claim.id, claim.text, toUnixMs(claim.created_at) ?? toUnixMs(claim.updated_at));
    }
  }
  for (const source of run.sources ?? []) {
    if (taskIds.has(source.produced_by_task_id ?? "")) {
      push(source.id, source.title, toUnixMs(source.created_at) ?? toUnixMs(source.retrieved_at));
    }
  }
  let best: { id: string; title: string; at: number } | undefined;
  for (const v of produced.values()) {
    if (!best || v.at > best.at) best = v;
  }
  return best ? { id: best.id, title: best.title, acceptedAt: best.at } : undefined;
}

export function buildExecutionOverlayRows(input: {
  members: readonly ResearchFleetMember[];
  presence: ResearchPresenceMap;
  nodes: readonly ResearchGraphNode[];
  run?: ResearchRunSnapshot | null;
  now?: number;
}): ExecutionRow[] {
  const now = input.now ?? Date.now();
  const nodesById = new Map((input.nodes ?? []).map((n) => [n.id, n]));
  const attempts = attemptsByAgent(input.run ?? undefined);
  // Map agent → their current canvas task node (from presence.nodeId, else the
  // node bound to the agent's latest task via attempt.task_id → payload).
  const attemptNodeByAgent = new Map<string, ResearchGraphNode>();
  for (const [agentId, list] of attempts) {
    const latest = list[list.length - 1];
    const node = latest?.task_id ? nodesById.get(latest.task_id) ?? findByTaskId(input.nodes ?? [], latest.task_id) : undefined;
    if (node) attemptNodeByAgent.set(agentId, node);
  }

  return (input.members ?? [])
    .filter((member) => member.status !== "archived")
    .map((member) => {
      const signal = input.presence[member.agent_id];
      const status = deriveStatus(signal, attempts.get(member.agent_id), now);
      const name = member.display_name || member.name || member.role || "Agent";
      const node = signal?.nodeId
        ? nodesById.get(signal.nodeId)
        : attemptNodeByAgent.get(member.agent_id);
      const attempt = attempts.get(member.agent_id)?.at(-1);
      const startedAt =
        toUnixMs(attempt?.started_at) ??
        toUnixMs(signal?.updatedAt) ??
        toUnixMs(signal?.expiresAt);
      const elapsedMs =
        startedAt != null && (status === "running" || status === "waiting" || status === "retrying")
          ? Math.max(0, now - startedAt)
          : undefined;
      const failureReason =
        status === "failed" && attempt ? attempt.failure_class : undefined;
      const waitingReason =
        status === "waiting"
          ? signal?.staleReason ??
            (signal?.phase === "idle" ? "idle" : signal?.phase === "queued" ? "queued" : undefined)
          : undefined;
      const recentResult = lastAcceptedResult(input.run ?? undefined, member.agent_id, attemptNodeByAgent);

      return {
        id: member.agent_id,
        name,
        role: member.role || signal?.role || "worker",
        initials: initials(name),
        avatarUrl: member.avatar_url ?? undefined,
        status,
        action: signal?.activity || undefined,
        actionKey: EXECUTION_STATUS_ACTION_KEY[status],
        actionDetail: signal?.activity || undefined,
        failureReasonKey: status === "failed" ? "failed" : undefined,
        reason: failureReason ?? waitingReason ?? signal?.staleReason ?? undefined,
        startedAt,
        updatedAt: toUnixMs(signal?.updatedAt) ?? now,
        elapsedMs,
        recentResult,
        currentNodeId: node?.id,
        locationLabel: node?.title || undefined,
        taskId: signal?.taskId ?? attempt?.task_id ?? null,
        attemptId: attempt?.id ?? null,
        branchId: signal?.branchId ?? null,
        stage: signal?.stage ?? null,
        staleReason: signal?.staleReason ?? null,
        waitingReason,
      };
    });
}

function findByTaskId(nodes: readonly ResearchGraphNode[], taskId: string): ResearchGraphNode | undefined {
  return nodes.find((node) => {
    const payload = node.payload as Record<string, unknown> | undefined;
    return payload?.task_id === taskId;
  });
}
