import { z } from "zod";

export type TrajectoryStatus = "running" | "success" | "detour" | "failed" | "merged";
export type TrajectoryRelationshipSource = "explicit" | "inferred_agent_sequence";

export interface TrajectoryParentRef {
  id: string;
  relationshipSource: TrajectoryRelationshipSource;
  unknown: boolean;
}

export interface TrajectoryCommit {
  id: string;
  parentIds: string[];
  unknownParentIds: string[];
  parentRefs: TrajectoryParentRef[];
  branchId: string;
  agentId: string | null;
  timestamp: string | null;
  title: string;
  summary: string;
  status: TrajectoryStatus;
  evidenceRefs: string[];
  relationshipSource: "none" | TrajectoryRelationshipSource | "mixed";
  sourceNodeIds: string[];
  taskId: string;
  attempt: number;
  sequence: number;
}

export interface TrajectoryGraph {
  commits: TrajectoryCommit[];
  relationshipIncomplete: boolean;
  warnings: string[];
  fallbackReason?: "schema_parse_failed";
}

export interface TrajectoryInputNode {
  id: string;
  taskId?: string;
  task_id?: string;
  attempt?: number;
  attempt_number?: number;
  sequence?: number;
  parentIds?: string[];
  parent_ids?: string[];
  branchId?: string;
  branch_id?: string;
  agentId?: string | null;
  actor_agent_id?: string | null;
  assigned_agent_id?: string | null;
  timestamp?: string;
  created_at?: string;
  title?: string;
  summary?: string;
  status?: string;
  nodeType?: string;
  node_type?: string;
  evidenceRefs?: string[];
  evidence_refs?: string[];
}

export interface TrajectoryInputEdge {
  from_node_id?: string;
  to_node_id?: string;
  parentId?: string;
  childId?: string;
}

export interface TrajectoryInput {
  nodes: TrajectoryInputNode[];
  edges?: TrajectoryInputEdge[];
}

const NodeSchema = z.object({
  id: z.string().min(1),
  taskId: z.string().optional(), task_id: z.string().optional(),
  attempt: z.number().optional(), attempt_number: z.number().optional(), sequence: z.number().optional(),
  parentIds: z.array(z.string()).optional(), parent_ids: z.array(z.string()).optional(),
  branchId: z.string().optional(), branch_id: z.string().optional(),
  agentId: z.string().nullable().optional(), actor_agent_id: z.string().nullable().optional(), assigned_agent_id: z.string().nullable().optional(),
  timestamp: z.string().optional(), created_at: z.string().optional(), title: z.string().optional(), summary: z.string().optional(), status: z.string().optional(),
  nodeType: z.string().optional(), node_type: z.string().optional(),
  evidenceRefs: z.array(z.string()).optional(), evidence_refs: z.array(z.string()).optional(),
}).passthrough();

const InputSchema = z.object({
  nodes: z.array(NodeSchema),
  edges: z.array(z.object({
    from_node_id: z.string().optional(), to_node_id: z.string().optional(),
    parentId: z.string().optional(), childId: z.string().optional(),
  }).passthrough()).optional().default([]),
}).passthrough();

const uniqueSorted = (values: string[]): string[] => [...new Set(values.filter(Boolean))].sort();
const numberOr = (value: number | undefined, fallback: number): number => Number.isFinite(value) ? value! : fallback;

function timestampValue(value: string | undefined): number {
  if (!value) return Number.POSITIVE_INFINITY;
  const parsed = Date.parse(value);
  return Number.isNaN(parsed) ? Number.POSITIVE_INFINITY : parsed;
}

function statusFor(node: z.infer<typeof NodeSchema>): TrajectoryStatus {
  const raw = node.status?.toLowerCase();
  if (raw === "merged") return "merged";
  if (["failed", "error", "cancelled"].includes(raw ?? "")) return "failed";
  if (["running", "active", "queued", "pending"].includes(raw ?? "")) return "running";
  if (["detour", "dead_end", "refuted", "abandoned"].includes(raw ?? "")) return "detour";
  if (["success", "succeeded", "completed", "done"].includes(raw ?? "")) return "success";
  const type = node.nodeType ?? node.node_type;
  if (type === "dead_end" || type === "refuted") return "detour";
  if (type === "finding" || type === "stage_gate") return "success";
  return "running";
}

function compareCommits(a: TrajectoryCommit, b: TrajectoryCommit): number {
  return timestampValue(a.timestamp ?? undefined) - timestampValue(b.timestamp ?? undefined)
    || a.sequence - b.sequence || a.id.localeCompare(b.id);
}

/** Normalize lineage or legacy graph JSON into a deterministic, UI-safe DAG. */
export function normalizeTrajectoryGraph(input: unknown): TrajectoryGraph {
  const parsed = InputSchema.safeParse(input);
  if (!parsed.success) return { commits: [], relationshipIncomplete: true, warnings: ["schema_parse_failed"], fallbackReason: "schema_parse_failed" };

  const warnings = new Set<string>();
  const rawNodes = [...parsed.data.nodes].sort((a, b) => a.id.localeCompare(b.id));
  const groups = new Map<string, typeof rawNodes>();
  for (const node of rawNodes) {
    const taskId = node.taskId ?? node.task_id ?? node.id;
    const key = `${taskId}\u0000${numberOr(node.attempt ?? node.attempt_number, 0)}\u0000${numberOr(node.sequence, 0)}`;
    groups.set(key, [...(groups.get(key) ?? []), node]);
  }

  const aliasToCanonical = new Map<string, string>();
  const commits: TrajectoryCommit[] = [];
  for (const group of groups.values()) {
    const canonical = group[0]!;
    const id = canonical.id;
    for (const node of group) aliasToCanonical.set(node.id, id);
    const agentId = group.map((node) => node.agentId ?? node.actor_agent_id ?? node.assigned_agent_id).find((value) => value != null) ?? null;
    if (!agentId) warnings.add("missing_agent");
    const taskId = canonical.taskId ?? canonical.task_id ?? canonical.id;
    commits.push({
      id, parentIds: [], unknownParentIds: [], parentRefs: [],
      branchId: canonical.branchId ?? canonical.branch_id ?? agentId ?? "unknown",
      agentId, timestamp: canonical.timestamp ?? canonical.created_at ?? null,
      title: group.map((node) => node.title).find(Boolean) ?? "Untitled trajectory event",
      summary: group.map((node) => node.summary).find(Boolean) ?? "",
      status: statusFor(canonical),
      evidenceRefs: uniqueSorted(group.flatMap((node) => node.evidenceRefs ?? node.evidence_refs ?? [])),
      relationshipSource: "none", sourceNodeIds: uniqueSorted(group.map((node) => node.id)),
      taskId, attempt: numberOr(canonical.attempt ?? canonical.attempt_number, 0), sequence: numberOr(canonical.sequence, 0),
    });
  }

  const byId = new Map(commits.map((commit) => [commit.id, commit]));
  const explicitParents = new Map<string, Set<string>>();
  for (const node of rawNodes) {
    const child = aliasToCanonical.get(node.id)!;
    for (const parent of node.parentIds ?? node.parent_ids ?? []) {
      const set = explicitParents.get(child) ?? new Set<string>(); set.add(parent); explicitParents.set(child, set);
    }
  }
  for (const edge of parsed.data.edges) {
    const parent = edge.parentId ?? edge.from_node_id;
    const childRaw = edge.childId ?? edge.to_node_id;
    if (!parent || !childRaw) continue;
    const child = aliasToCanonical.get(childRaw) ?? childRaw;
    const set = explicitParents.get(child) ?? new Set<string>(); set.add(parent); explicitParents.set(child, set);
  }

  const shouldInfer = parsed.data.edges.length === 0 && explicitParents.size === 0;
  if (shouldInfer) {
    const branches = new Map<string, TrajectoryCommit[]>();
    for (const commit of commits) if (commit.agentId) branches.set(commit.agentId, [...(branches.get(commit.agentId) ?? []), commit]);
    for (const branch of branches.values()) {
      branch.sort(compareCommits);
      for (let index = 1; index < branch.length; index++) explicitParents.set(branch[index]!.id, new Set([branch[index - 1]!.id]));
    }
  }

  for (const commit of commits) {
    const refs = [...(explicitParents.get(commit.id) ?? [])].map((rawId): TrajectoryParentRef => {
      const id = aliasToCanonical.get(rawId) ?? rawId;
      return { id, relationshipSource: shouldInfer ? "inferred_agent_sequence" : "explicit", unknown: !byId.has(id) };
    }).sort((a, b) => Number(a.unknown) - Number(b.unknown) || a.id.localeCompare(b.id));
    commit.parentRefs = refs;
    commit.parentIds = uniqueSorted(refs.filter((ref) => !ref.unknown && ref.id !== commit.id).map((ref) => ref.id));
    commit.unknownParentIds = uniqueSorted(refs.filter((ref) => ref.unknown || ref.id === commit.id).map((ref) => ref.id));
    commit.relationshipSource = refs.length === 0 ? "none" : refs[0]!.relationshipSource;
  }

  const ordered: TrajectoryCommit[] = [];
  const remaining = new Map(commits.map((commit) => [commit.id, commit]));
  while (remaining.size > 0) {
    const ready = [...remaining.values()].filter((commit) => commit.parentIds.every((id) => !remaining.has(id))).sort(compareCommits);
    if (ready.length === 0) {
      warnings.add("cycle_detected");
      for (const commit of [...remaining.values()].sort(compareCommits)) {
        const cyclic = commit.parentIds.filter((id) => remaining.has(id));
        commit.parentIds = commit.parentIds.filter((id) => !remaining.has(id));
        commit.unknownParentIds = uniqueSorted([...commit.unknownParentIds, ...cyclic]);
        commit.parentRefs = commit.parentRefs.map((ref) => cyclic.includes(ref.id) ? { ...ref, unknown: true } : ref);
      }
      continue;
    }
    for (const commit of ready) { ordered.push(commit); remaining.delete(commit.id); }
  }

  return { commits: ordered, relationshipIncomplete: shouldInfer || ordered.some((commit) => commit.unknownParentIds.length > 0) || warnings.has("cycle_detected"), warnings: [...warnings].sort() };
}
