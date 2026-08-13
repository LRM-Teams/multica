/**
 * LRM-1496 — D5 five-level node visual system · data contract.
 *
 * This module lives in `packages/views/research/star-graph/lib` on purpose:
 * it is the boundary between canonical research data and the shareable
 * presentation surface in `packages/ui/components/star-graph`. No business
 * logic enters `packages/ui`; all reading, mapping and degradation happens
 * HERE.
 *
 * Inputs:
 *   - `StarGraphNodeInput` — a minimal, canonical-projection-shaped node.
 *     We accept the real node plus an optional `typed` block that, once
 *     LRM-1505 (typed graph model: level/round/cluster_id/document_count/
 *     conclusion_count) lands on `dev`, carries the backend-typed fields.
 *
 * RED LINE (supervisor directive): when the typed fields are absent the
 * front-end MUST NOT fabricate values. We fall back to the *real* `node_kind`
 * / `status` / `importance` that already exist on `dev`. If even those do not
 * identify a tier, we degrade to the safe `result` (mid) tier — never a fake
 * number.
 */

import type { StarGraphNodeState } from "@multica/ui/components/star-graph";
import type { StarGraphTier } from "@multica/ui/components/star-graph";

/**
 * LRM-1505 typed fields as consumed once they land on `dev`. Fields are
 * optional because during the pre-1505 window the projection does not carry
 * them. When present they are authoritative; when absent the adapter falls
 * back to `node_kind` + `importance` classification.
 */
export interface StarGraphTypedFields {
  /** Backend-assigned 5-level tier ("xxl" | "xl" | "l" | "m" | "s"). */
  level?: StarGraphTier | string;
  /** Integration/round label (e.g. 2). */
  round?: number | string | null;
  /** Cluster id for grouping. */
  cluster_id?: string | null;
  /** Source/supporting document count. */
  document_count?: number | null;
  /** Confidence as canonical 0..1, or legacy percent 0..100. */
  confidence?: number | null;
  /** Conclusion/count of findings. */
  conclusion_count?: number | null;
}

/** Canonical-projection-shaped minimal node the adapter reads. */
export interface StarGraphNodeInput {
  id: string;
  node_kind: string;
  status: string;
  /** 1..3 importance as projected. */
  importance?: number | null;
  title: string;
  /** Bounded summary — real value, may be empty. */
  summary?: string;
  /** Agent short handle for S-tier nodes (real value, may be null). */
  actor_agent_id?: string | null;
  /** Opaque detail payload — only exact, known keys are read; never guessed. */
  detail?: unknown;
  /** LRM-1505 typed block (absent until the typed model lands on dev). */
  typed?: StarGraphTypedFields;
}

/** The presentation props the node surface consumes (subset of NodeProps). */
export interface StarGraphNodeView {
  id: string;
  tier: StarGraphTier;
  tierSource: "typed" | "kind-classified" | "fallback";
  state: StarGraphNodeState;
  title: string;
  subLabel?: string;
  headerLabel?: string;
  agentBadge?: string;
  metrics?: {
    documentCount?: number;
    confidence?: number;
    conclusionCount?: number;
    round?: string;
  };
}
