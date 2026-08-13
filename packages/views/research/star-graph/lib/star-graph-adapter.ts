/**
 * LRM-1496 — D5 five-level node visual system · adapter.
 *
 * Maps a real canonical projection node into the presentation props consumed
 * by `StarGraphNode` in `packages/ui/components/star-graph`. This file is the
 * ONLY place tier / state / metric mapping happens; `packages/ui` receives
 * already-shaped props and stays free of domain logic.
 *
 * Degradation order (supervisor red line — never fabricate):
 *   1. typed `level` from LRM-1505 (when it lands on `dev`) → authoritative.
 *   2. else classify from the REAL `node_kind` that already exists on `dev`.
 *   3. else safe mid tier `result` (unknown kind) — the node still renders
 *      with its real title; no invented numbers.
 *
 * State maps from the real `status` string; metrics (document_count /
 * confidence / conclusion_count) are only emitted when the field is present
 * on the node — never synthesised.
 */

import {
  familyForNodeKind,
  type NodeKindFamily,
} from "../../node-renderers/node-kind-registry";

import type {
  StarGraphNodeInput,
  StarGraphNodeView,
  StarGraphTypedFields,
} from "./star-graph-contract";
import type { StarGraphTier } from "@multica/ui/components/star-graph";
import type { StarGraphNodeState } from "@multica/ui/components/star-graph";

/* ------------------------------------------------------------------ *
 * Tier classification
 * ------------------------------------------------------------------ */

/**
 * Map the canonical 6-card family onto the D5 five-tier system.
 * Uses the REAL family resolved from node_kind (never guesses kind).
 *
 *  - structure/governance  → large result surfaces (L/M) by importance
 *  - cognition (insight/claim/decision) → conclusion tiers (M/xl)
 *  - execution/collaboration → running agent (S)
 */
function tierForFamily(
  family: NodeKindFamily,
  importance: number | null | undefined,
): StarGraphTier {
  const imp = Number(importance);
  const strong = !Number.isNaN(imp) && imp >= 3;

  switch (family) {
    case "cognition":
      // conclusions/insights — XL when strongly important, else M
      return strong ? "xl" : "m";
    case "evidence":
      // supporting evidence — M/L intermediate
      return "m";
    case "structure":
      // plan/objective surfaces — L head, M otherwise
      return strong ? "l" : "m";
    case "governance":
      // report/umbrella — the synthesis umbrella maps to top tier family.
      // We keep it on the large-result surface (L) unless importance is
      // maximal, which the D5 spec ties to the final synthesis (XXL).
      return strong ? "xxl" : "l";
    case "execution":
      // one running action = agent dot (S)
      return "s";
    case "collaboration":
      // team/integration ephemeral = agent dot (S)
      return "s";
    case "generic":
    default:
      // unknown future kind — safe mid tier, node still renders with real
      // title; never crashes, never fabricates.
      return "m";
  }
}

/** Validate a typed level string against the five known tiers. */
function typedTier(level: unknown): StarGraphTier | null {
  const candidate = typeof level === "string" ? level.trim().toLowerCase() : null;
  return candidate === "xxl" || candidate === "xl" || candidate === "l" ||
    candidate === "m" || candidate === "s"
    ? candidate
    : null;
}

/** Resolve tier following the degradation chain (supervisor red line). */
export function resolveTier(node: StarGraphNodeInput): {
  tier: StarGraphTier;
  source: "typed" | "kind-classified" | "fallback";
} {
  const typed = typedTier(node.typed?.level);
  if (typed) return { tier: typed, source: "typed" };

  const family = familyForNodeKind(node.node_kind);
  // familyForNodeKind already yields "generic" for unknown kinds; the `episode`
  // umbrella is classified under `governance` by the registry and mapped to the
  // top tier below. Unknown kinds therefore fall straight out to the safe mid
  // tier — no fabricated values.
  if (family === "generic") {
    return { tier: "m", source: "fallback" };
  }
  return { tier: tierForFamily(family, node.importance), source: "kind-classified" };
}

/* ------------------------------------------------------------------ *
 * State mapping (real status → StarGraphNodeState)
 * ------------------------------------------------------------------ */

/** Map the real projection `status` string onto the D5 state surface. */
export function mapNodeState(status: string): StarGraphNodeState {
  const s = (status || "").trim().toLowerCase();
  switch (s) {
    case "running":
    case "in_progress":
    case "working":
    case "dispatching":
    case "claimed":
    case "in_flight":
    case "queued":
      return "run";
    case "failed":
    case "error":
      return "failed";
    case "stale":
    case "superseded":
    case "abandoned":
    case "deprecated":
    case "cancelled":
    case "obsolete":
      return "abandoned";
    case "pending":
    case "pending_review":
    case "review":
    case "waiting":
      return "pending-review";
    case "conflict":
    case "conflicted":
      return "conflict";
    case "restart":
    case "restarting":
    case "restarted":
      return "restart";
    case "done":
    case "completed":
    case "terminal":
    case "accepted":
    case "succeeded":
    case "success":
    case "resolved":
      return "stable";
    case "idle":
    case "ready":
    default:
      return "default";
  }
}

/* ------------------------------------------------------------------ *
 * Metrics — ALWAYS from present fields, never synthesised.
 * ------------------------------------------------------------------ */

function safeCount(value: unknown): number | undefined {
  return typeof value === "number" && !Number.isNaN(value) && value >= 0
    ? value
    : undefined;
}

function confidencePercent(value: unknown): number | undefined {
  if (typeof value !== "number" || !Number.isFinite(value)) return undefined;
  if (value < 0 || value > 100) return undefined;
  return Math.round(value <= 1 ? value * 100 : value);
}

/** Read metrics ONLY from fields that actually exist (typed or detail). */
export function mapMetrics(
  typed: StarGraphTypedFields | undefined,
): StarGraphNodeView["metrics"] {
  const metrics: NonNullable<StarGraphNodeView["metrics"]> = {};
  const doc = safeCount(typed?.document_count);
  if (doc != null) metrics.documentCount = doc;
  const conf = confidencePercent(typed?.confidence);
  if (conf != null) metrics.confidence = conf;
  const concl = safeCount(typed?.conclusion_count);
  if (concl != null) metrics.conclusionCount = concl;
  const round = typeof typed?.round === "string" || typeof typed?.round === "number"
    ? String(typed.round)
    : undefined;
  if (round) metrics.round = round;
  return Object.keys(metrics).length ? metrics : undefined;
}

function subLabelForTier(
  node: StarGraphNodeInput,
): string | undefined {
  // The prototype's larger result nodes carry a concise explanatory line.
  // Keep that information density in production only when the projection
  // supplied the fact; an absent summary remains absent rather than being
  // reconstructed from the title or node kind.
  return typeof node.summary === "string" && node.summary.trim()
    ? node.summary.trim()
    : undefined;
}

/* ------------------------------------------------------------------ *
 * Top-level adapter
 * ------------------------------------------------------------------ */

/**
 * Adapter entry: real projection node → D5 view props.
 * `tierSource` is exposed so tests and the map key / guide can tell whether
 * the node used typed LRM-1505 fields, a kind classification, or the safe
 * fallback — proving no values were fabricated.
 */
export function toStarGraphNodeView(node: StarGraphNodeInput): StarGraphNodeView {
  const { tier, source } = resolveTier(node);
  // Real metrics: preferred from typed fields, else none — we cannot read
  // document_count from a pre-1505 projection without inventing it, so the
  // metrics are simply omitted until typed data exists.
  const metrics = mapMetrics(node.typed);

  const mappedState = mapNodeState(node.status);
  // The run projection uses `active` for an execution node that is currently
  // working, while an active root/result is not necessarily executing. Keep
  // that distinction at the tier boundary instead of pulsing the whole graph.
  const state =
    tier === "s" && (node.status ?? "").trim().toLowerCase() === "active"
      ? "run"
      : mappedState;
  if (tier === "s") {
    return {
      id: node.id,
      tier,
      tierSource: source,
      state,
      title: node.title,
      subLabel: subLabelForTier(node),
      agentBadge:
        typeof node.actor_agent_id === "string" && node.actor_agent_id
          ? shortAgent(node.actor_agent_id)
          : undefined,
      metrics,
    };
  }

  return {
    id: node.id,
    tier,
    tierSource: source,
    state,
    title: node.title,
    subLabel: subLabelForTier(node),
    metrics,
  };
}

/** Trim an agent id like "agent:lindberg" → "L" (or short handle). Real value. */
function shortAgent(raw: string): string {
  const handle = raw.split(":")[1] ?? raw;
  return handle.length <= 4 ? handle.toUpperCase() : handle.slice(0, 3).toUpperCase();
}
