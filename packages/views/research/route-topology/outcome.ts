/**
 * Read-only outcome classification (LRM-1487 / 实现-11).
 *
 * Satisfaction of spec §3 and AC3:
 *   - outcomes are classified ONLY from explicit, verbatim projection fields —
 *     node `status`, attempt phase/status lifted from node `payload`, and edge
 *     `relation`. Title / summary / agent prose are NEVER parsed.
 *   - unknown status strings and unknown relations degrade to `neutral`.
 *   - an edge with `supports` does NOT auto-accept, and `produced` does NOT
 *     auto-succeed — acceptance only follows an accepted node status.
 *   - node/attempt outcome and edge/relation outcome are kept distinct so a
 *     conflict (e.g. a `contradicts` relation pointing at an `accepted` node)
 *     preserves BOTH encodings; the renderer may show different color + line
 *     style + endpoint independently.
 */
import type { CanvasSlice } from "@multica/core/adapters";
import type { OutcomeRegistry, RouteOutcome } from "./types";

/* ---------------------------------------------------------------------------
 * Explicit known-state registries. Only these strings are recognised; anything
 * else is neutral. Keeping them centralized makes the "read-only explicit"
 * guarantee auditable.
 * ------------------------------------------------------------------------- */

/** Node / attempt explicit terminal-failure states. */
export const KNOWN_TERMINAL_FAILED = new Set<string>([
  "failed",
  "lost",
  "error",
  "aborted",
  "rejected",
]);

/** Attempt explicit cancellation states. */
export const KNOWN_CANCELLED = new Set<string>([
  "cancelling",
  "cancelled",
  "canceled",
  "stopped",
]);

/** Node / attempt explicit stale/invalidated states. */
export const KNOWN_STALE = new Set<string>([
  "stale",
  "invalidated",
  "expired",
  "superseded",
  "deprecated",
]);

/** Node explicit disputed/challenged states. */
export const KNOWN_DISPUTED = new Set<string>([
  "disputed",
  "challenged",
  "contested",
  "refuted",
]);

/** Node explicit accepted/resolved states. */
export const KNOWN_ACCEPTED = new Set<string>([
  "accepted",
  "resolved",
  "done",
  "approved",
  "confirmed",
  "succeeded",
]);

/** Node explicit in-flight / exploring states. */
export const KNOWN_EXPLORING = new Set<string>([
  "exploring",
  "active",
  "running",
  "in_progress",
  "in-progress",
  "queued",
  "dispatched",
  "pending",
  "working",
]);

/** Edge relations that carry a dispute semantic. */
export const KNOWN_DISPUTE_RELATION = new Set<string>([
  "contradicts",
  "challenged_by",
  "disputes",
  "refutes",
]);

/** Edge relations that carry a stale/history semantic (never delete history). */
export const KNOWN_INVALIDATE_RELATION = new Set<string>([
  "invalidates",
  "supersedes",
  "invalidated_by",
  "retired_after",
]);

/* ---------------------------------------------------------------------------
 * Registry construction — read-only, verbatim.
 * ------------------------------------------------------------------------- */

/** Lift an attempt phase/status from a node payload when present (best-effort). */
function attemptStatusFromPayload(payload: Record<string, unknown>): string | undefined {
  if (!payload || typeof payload !== "object") return undefined;
  const candidate =
    payload.attempt_status ??
    payload.attemptStatus ??
    payload.phase ??
    payload.status;
  return typeof candidate === "string" ? candidate : undefined;
}

/** Build the read-only outcome registry from a bounded slice. */
export function buildOutcomeRegistry(slice: CanvasSlice): OutcomeRegistry {
  const nodeStatus = new Map<string, string>();
  const attemptStatus = new Map<string, string>();
  const relationByEdge = new Map<string, string>();
  for (const n of slice.nodes) {
    nodeStatus.set(n.id, n.status);
    const attempt = attemptStatusFromPayload(n.payload);
    if (attempt !== undefined) attemptStatus.set(n.id, attempt);
  }
  for (const e of slice.edges) {
    relationByEdge.set(e.id, e.relation);
  }
  return { nodeStatus, attemptStatus, relationByEdge };
}

/* ---------------------------------------------------------------------------
 * Classifiers — pure, deterministic, registry-only.
 * ------------------------------------------------------------------------- */

/** Classify a single verbatim status string (node or attempt). */
export function classifyStatus(status: string): RouteOutcome {
  if (KNOWN_TERMINAL_FAILED.has(status)) return "failed";
  if (KNOWN_CANCELLED.has(status)) return "cancelled";
  if (KNOWN_STALE.has(status)) return "stale";
  if (KNOWN_DISPUTED.has(status)) return "disputed";
  if (KNOWN_ACCEPTED.has(status)) return "accepted";
  if (KNOWN_EXPLORING.has(status)) return "exploring";
  return "neutral";
}

/**
 * Node/attempt endpoint outcome: derived from the node's explicit status and
 * (when present) the attempt phase lifted from payload. This is the "node"
 * side of the dual encoding — a route bundle uses it for a node's own shape.
 */
export function classifyNodeOutcome(nodeId: string, registry: OutcomeRegistry): RouteOutcome {
  const attempt = registry.attemptStatus.get(nodeId);
  if (attempt !== undefined) {
    const a = classifyStatus(attempt);
    if (a !== "neutral") return a;
  }
  const status = registry.nodeStatus.get(nodeId);
  if (status === undefined) return "neutral";
  return classifyStatus(status);
}

/**
 * Edge/relation outcome: derived from the edge relation and its TARGET node
 * outcome. `supports`/`produced` never auto-accept — acceptance only follows an
 * accepted target status.
 */
export function classifyEdgeOutcome(edge: {
  id: string;
  from: string;
  to: string;
  relation: string;
}, registry: OutcomeRegistry): RouteOutcome {
  if (KNOWN_DISPUTE_RELATION.has(edge.relation)) return "disputed";
  if (KNOWN_INVALIDATE_RELATION.has(edge.relation)) return "stale";
  // Otherwise the edge's traversal outcome is inherited from its target node's
  // explicit outcome — never from the relation name or prose.
  return classifyNodeOutcome(edge.to, registry);
}

/**
 * Combined route outcome for a (node, edge) tuple (~spec §10 signature).
 * The node is the semantic anchor; when the edge carries an explicit
 * dispute/invalidate relation it overrides to that state. Node and edge
 * outcomes remain separately available via the two classifiers above, so
 * conflicts are preserved, not flattened.
 */
export function classifyRouteOutcome(
  node: { id: string } | null,
  edge: { id: string; from: string; to: string; relation: string } | null,
  registry: OutcomeRegistry,
): RouteOutcome {
  const base = node ? classifyNodeOutcome(node.id, registry) : "neutral";
  if (edge) {
    if (KNOWN_DISPUTE_RELATION.has(edge.relation)) return "disputed";
    if (KNOWN_INVALIDATE_RELATION.has(edge.relation)) return "stale";
  }
  return base;
}

/** Convenience: node outcome + edge outcome kept distinct (dual encoding). */
export function classifyPair(
  node: { id: string } | null,
  edge: { id: string; from: string; to: string; relation: string } | null,
  registry: OutcomeRegistry,
): { nodeOutcome: RouteOutcome; edgeOutcome: RouteOutcome } {
  return {
    nodeOutcome: node ? classifyNodeOutcome(node.id, registry) : "neutral",
    edgeOutcome: edge ? classifyEdgeOutcome(edge, registry) : "neutral",
  };
}
