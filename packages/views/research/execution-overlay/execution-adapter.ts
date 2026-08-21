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

  /** Current task objective/title (agent-execution-spec §1 task row). */
  taskObjective?: string;

  /** Absolute start time (unix ms) of the current work, when known. */
  startedAt?: number;
  /** Last canonical update (unix ms); absent when the projection omitted it. */
  updatedAt?: number;
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

function toUnixMs(value: number | string | null | undefined): number | undefined {
  if (value == null) return undefined;
  if (typeof value === "number") return Number.isFinite(value) ? value : undefined;
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

/**
 * True when the agent is retrying: a newer in-flight attempt follows a failed
 * terminal attempt for the same agent (contract §2 — only with ledger facts).
 */
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

function isCancelling(attempts: ResearchRunAttempt[] | undefined): boolean {
  // Contract §2: `cancelling` = cancellation requested, not yet terminal.
  // Only when the attempt ledger carries an in-flight cancellation fact
  // (status "cancelling" before a terminal failed/lost/cancelled).
  if (!attempts || attempts.length === 0) return false;
  const last = attempts[attempts.length - 1]!;
  return last.status === "cancelling";
}

function isTerminalFailure(status: string | null | undefined): boolean {
  return status === "failed" || status === "lost" || status === "cancelled";
}

type PresenceLike = ResearchPresenceMap[string] | undefined;

function deriveStatus(
  presence: PresenceLike,
  attempts: ResearchRunAttempt[] | undefined,
  now: number,
  presenceAvailable: boolean,
): ExecutionStatus {
  if (!presence) return presenceAvailable ? "offline" : "unknown";
  if (presence.updatedAt == null) return "unknown";
  const phase = presence.phase;
  if (phase === "stale") return "stale";
  // Cancellation in flight wins over a stale classification only when the
  // attempt ledger carries the fact; otherwise a cancelled terminal attempt
  // still reports its preceding presence phase below.
  if (isCancelling(attempts)) return "cancelling";
  if (phase === "failed") {
    return isRetrying(presence, attempts) ? "retrying" : "failed";
  }
  if (phase === "running" || phase === "queued") {
    // Contract: queued/running past expires_at → stale.
    if (presence.expiresAt != null && now >= presence.expiresAt) return "stale";
    // presence running → running (unexpired); queued → queued (task assigned,
    // not yet runtime-started; never fake a running timer).
    return phase === "running" ? "running" : "queued";
  }
  if (phase === "done") return "done";
  // Roster present with an active presence entry and no running/queued
  // evidence → idle (contract §2): never infer offline from absence of
  // activity, and never fake running.
  if (phase === "idle") return "idle";
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
  /** False only when no successful presence projection is available. */
  presenceAvailable?: boolean;
  nodes: readonly ResearchGraphNode[];
  run?: ResearchRunSnapshot | null;
  now?: number;
}): ExecutionRow[] {
  const now = input.now ?? Date.now();
  const presenceAvailable = input.presenceAvailable ?? true;
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
      const status = deriveStatus(
        signal,
        attempts.get(member.agent_id),
        now,
        presenceAvailable,
      );
      const name = member.display_name || member.name || member.role || "Agent";
      const node = signal?.nodeId
        ? nodesById.get(signal.nodeId)
        : attemptNodeByAgent.get(member.agent_id);
      const attempt = attempts.get(member.agent_id)?.at(-1);
      const task = input.run?.tasks?.find((t) => t.id === (signal?.taskId ?? attempt?.task_id));
      const startedAt =
        toUnixMs(attempt?.started_at) ??
        toUnixMs(signal?.updatedAt) ??
        toUnixMs(signal?.expiresAt);
      const elapsedMs =
        startedAt != null && (status === "running" || status === "queued" || status === "cancelling" || status === "retrying")
          ? Math.max(0, now - startedAt)
          : undefined;
      const failureReason =
        status === "failed" && attempt ? attempt.failure_class : undefined;
      const waitingReason =
        status === "queued" || status === "idle"
          ? signal?.staleReason ?? (signal?.phase === "idle" ? "idle" : signal?.phase === "queued" ? "queued" : undefined)
          : undefined;
      const recentResult = lastAcceptedResult(input.run ?? undefined, member.agent_id, attemptNodeByAgent);

      return {
        id: member.agent_id,
        name,
        role: member.role || signal?.role || "worker",
        avatarUrl: member.avatar_url ?? undefined,
        status,
        action: signal?.activity || undefined,
        actionKey: EXECUTION_STATUS_ACTION_KEY[status],
        actionDetail: signal?.activity || undefined,
        failureReasonKey: status === "failed" ? "failed" : undefined,
        reason: failureReason ?? waitingReason ?? signal?.staleReason ?? undefined,
        startedAt,
        updatedAt: toUnixMs(signal?.updatedAt),
        elapsedMs,
        taskObjective: task?.objective ?? task?.expected_result ?? undefined,
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
