/**
 * Unified research-canvas projection model (FE-04).
 *
 * The V5 graph adapter and Director V6 view adapter both target this
 * render-layer shape. Director's authoritative protocol remains isolated in
 * its own projection module; this file contains no server wire contract.
 *
 * Field provenance contract (§7.1 of the autonomous-research-system plan):
 *   - `id`            → stable projection node id derived from
 *                        the server node id (V5) or Director projection id.
 *   - `kind/subtype`  → normalized projection kind/subtype. Unknown future
 *                        kinds degrade to `kind:"generic"`.
 *   - `title/summary` → backend title + bounded summary. Never parsed.
 *   - `status`        → backend status verbatim.
 *   - `importance`    → 0..1, from a documented backend field when present,
 *                        else a stable neutral default (never derived from
 *                        prose).
 *   - `freshness`     → verbatim server signal (`string | null`) or a documented
 *                        V5 timestamp-derived recency (number).
 *   - `detailRef`     → stable canonical entity reference for the by-id detail
 *                        read. Never fabricated.
 */

/** Stable projection node identity — opaque, renderers must not parse it. */
export type CanvasNodeId = string;

/** Typed edge relation — one of the stable relation families in §7.1. */
export type CanvasRelation = string;

export interface CanvasNode {
  /** Stable projection identity (see header contract). */
  id: CanvasNodeId;
  /** Normalized projection kind, or "generic" for unknown future kinds. */
  kind: string;
  /** node_subtype when the backend provides one. */
  subtype?: string;
  /** Backend schema version when provided. */
  schemaVersion?: string;
  title: string;
  /** Bounded summary — display copy only, never a research fact source. */
  summary: string;
  status: string;
  level?: "xxl" | "xl" | "l" | "m" | "s";
  clusterId?: string | null;
  parentId?: string | null;
  round?: number;
  confidence?: number | null;
  documentCount?: number | null;
  conclusionCount?: number | null;
  derivedFrom?: string | null;
  mergedFrom?: string[];
  supersededBy?: string | null;
  restartOf?: string | null;
  invalidatedBy?: string | null;
  /** 0..1 importance rank (documented field or stable neutral). */
  importance: number;
  /**
   * 0..1 freshness (number), the server's opaque freshness token (string), or
   * null when the backend reports none — copied verbatim, never derived from
   * prose.
   */
  freshness: number | string | null;
  /** Stable canonical entity reference for the by-id detail read. */
  detailRef: string;
  /** Actor Agent id when the projection reports one. */
  actor?: string | null;
  /** Contract/plan/strategy version binding when the projection reports one. */
  planVersion?: string | null;
  /** created / updated event sequences when the projection reports them. */
  createdAtSequence?: number;
  updatedAtSequence?: number;
  /** Raw node_kind union for generic degradation and typed styling. */
  payload: Record<string, unknown>;
  /** Backend timestamps verbatim (`null` when the projection reports none). */
  createdAt: string | null;
  updatedAt: string | null;
}

export interface CanvasCluster {
  id: string;
  label: string;
  clusterType: string;
  memberNodeIds: string[];
  confidence: number | null;
  documentCount: number | null;
  conclusionCount: number | null;
}

export interface CanvasEdge {
  /** Stable edge identity (idempotent upserts across deltas). */
  id: string;
  from: CanvasNodeId;
  to: CanvasNodeId;
  /** Typed relation, verbatim from the projection. */
  relation: CanvasRelation;
  /**
   * Backend timestamp (string) or event sequence (number) verbatim; `null`
   * when the projection reports neither. Never fabricated.
   */
  createdAt: number | string | null;
}

/**
 * Server-committed snapshot (§7.2). All pages of one logical snapshot share
 * the same `snapshotId` + `throughEventSequence` + `graphContentHash`.
 */
export interface CanvasSnapshot {
  snapshotId: string;
  throughEventSequence: number;
  graphContentHash: string;
  nodes: CanvasNode[];
  edges: CanvasEdge[];
  clusters?: CanvasCluster[];
}

/**
 * Incremental projection delta (§7.2).
 *   - `fromSequenceExclusive` … `throughSequence` frame the delta.
 *   - `upsert*` are idempotent by stable id (duplicate deltas never duplicate).
 *   - `tombstone*` are visibility tombstones: they remove view nodes and
 *     dangling edges; the client must only recompute the affected subgraphs.
 *   - `affectedRootIds` are the canonical roots annexed by this delta.
 *   - `transitionKind` expresses the committed semantic change only (no
 *     coordinates/animation), one of the §7.2 transition kinds.
 */
export interface CanvasDelta {
  fromSequenceExclusive: number;
  throughSequence: number;
  upsertNodes: CanvasNode[];
  upsertEdges: CanvasEdge[];
  tombstoneNodeIds: CanvasNodeId[];
  tombstoneEdgeIds: string[];
  upsertClusters?: CanvasCluster[];
  tombstoneClusterIds?: string[];
  affectedRootIds: CanvasNodeId[];
  transitionKind: CanvasTransitionKind;
}

/** §7.2 committed transition kinds — semantic only. */
export type CanvasTransitionKind =
  | "branch_spawned"
  | "task_dispatched"
  | "result_accepted"
  | "integration_formed"
  | "insight_staled"
  | "dispute_opened"
  | "deliberation_progressed"
  | "lead_escalated"
  | "team_membership_changed"
  | "report_revised";

/**
 * A bounded projection slice (§7.2). Every node also carries the unloaded
 * neighborhood / descendant / expandability hints shown to the renderer so it
 * can decide how much to fetch without inferring graph structure.
 */
export interface CanvasSlice {
  rootId: CanvasNodeId;
  direction: "out" | "in" | "both";
  relationTypes?: readonly string[];
  maxDepth?: number;
  statusFilter?: readonly string[];
  importanceFloor?: number;
  nodes: CanvasNode[];
  edges: CanvasEdge[];
  unloadedCountByNode: Record<string, number>;
  descendantCountByNode: Record<string, number>;
  expandableByNode: Record<string, boolean>;
}
