import type {
  ResearchFleetMember,
  ResearchGraphNode,
  ResearchRunGateFinding,
  ResearchRunSnapshot,
} from "@multica/core/types";

export type RunV2GateBlocker = {
  id: string;
  label: string;
  targetNodeId: string | null;
};

export type RunV2CanvasViewModel = {
  nodes: ResearchGraphNode[];
  blockers: RunV2GateBlocker[];
  degraded: boolean;
};

function record(value: unknown): Record<string, unknown> | null {
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null;
}

function stringField(value: unknown, key: string): string | null {
  const candidate = record(value)?.[key];
  return typeof candidate === "string" && candidate.trim() ? candidate : null;
}

function projectedEntity(node: ResearchGraphNode): { kind: string; id: string } | null {
  const payload = record(node.payload);
  if (payload?.projection !== "run_v2") return null;
  const kind = stringField(payload, "kind");
  if (!kind) return null;
  const id = stringField(payload, `${kind}_id`);
  return id ? { kind, id } : null;
}

function targetEntity(finding: ResearchRunGateFinding): { kind: string; id: string } | null {
  const metadata = record(finding.metadata);
  for (const kind of ["task", "question", "attempt", "claim"] as const) {
    const id = stringField(metadata, `${kind}_id`);
    if (id) return { kind, id };
  }
  return null;
}

function memberName(members: readonly ResearchFleetMember[], agentId: string): string {
  const member = members.find((candidate) => candidate.agent_id === agentId);
  return member?.display_name || member?.name || member?.role || agentId;
}

function durationLabel(startedAt: string | undefined, now: number): string | null {
  if (!startedAt) return null;
  const started = Date.parse(startedAt);
  if (!Number.isFinite(started)) return null;
  const minutes = Math.max(0, Math.floor((now - started) / 60_000));
  return minutes < 1 ? "<1 min" : `${minutes} min`;
}

/**
 * Narrow adapter from the canonical run-v2 ledger to existing canvas cards.
 * Topology stays server-owned: this function never creates nodes or edges.
 */
export function buildRunV2CanvasViewModel(
  nodes: readonly ResearchGraphNode[],
  run: ResearchRunSnapshot | undefined,
  members: readonly ResearchFleetMember[],
  now = Date.now(),
): RunV2CanvasViewModel {
  if (!run) return { nodes: [...nodes], blockers: [], degraded: false };

  const taskById = new Map(run.tasks.map((task) => [task.id, task]));
  const latestAttemptByTask = new Map<string, ResearchRunSnapshot["attempts"][number]>();
  for (const attempt of run.attempts) {
    const current = latestAttemptByTask.get(attempt.task_id);
    if (!current || attempt.attempt_number > current.attempt_number) {
      latestAttemptByTask.set(attempt.task_id, attempt);
    }
  }

  const entityNode = new Map<string, string>();
  let projectedCount = 0;
  const adapted = nodes.map((node) => {
    const entity = projectedEntity(node);
    if (!entity) return node;
    projectedCount += 1;
    entityNode.set(`${entity.kind}:${entity.id}`, node.id);
    if (entity.kind !== "task" && entity.kind !== "attempt") return node;

    const task = entity.kind === "task" ? taskById.get(entity.id) : undefined;
    const attempt =
      entity.kind === "attempt"
        ? run.attempts.find((item) => item.id === entity.id)
        : task
          ? latestAttemptByTask.get(task.id)
          : undefined;
    const agentId = attempt?.assigned_agent_id || task?.assigned_agent_id || node.actor_agent_id;
    const execution = {
      agent: agentId ? memberName(members, agentId) : null,
      status: attempt?.status || task?.status || node.status,
      duration: durationLabel(attempt?.started_at || task?.started_at, now),
      failure: attempt?.diagnostics || attempt?.failure_class || task?.terminal_reason || null,
    };
    const payload = record(node.payload) ?? {};
    return { ...node, payload: { ...payload, execution } };
  });

  const blockers = run.gate.findings.map((finding, index) => {
    const target = targetEntity(finding);
    return {
      id: `${finding.code}:${index}`,
      label: finding.message,
      targetNodeId: target ? entityNode.get(`${target.kind}:${target.id}`) ?? null : null,
    };
  });

  return {
    nodes: adapted,
    blockers,
    degraded: projectedCount === 0 && (run.questions.length > 0 || run.tasks.length > 0),
  };
}
