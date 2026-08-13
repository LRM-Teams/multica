"use client";

/**
 * Research Session data-entry adapter (LRM-1484 / FE-08).
 *
 * Bridges the selected backend (V5 session snapshot or V6 projection) into the
 * unified render-layer `CanvasSnapshot`, consuming ONLY the committed core
 * adapters (`@multica/core/adapters`). Production renderers must not read the
 * V5/V6 wire shapes — everything goes through here (data-contract §1).
 *
 * Field provenance is enforced by the core adapters (canvas-types.ts header):
 * no field is guessed, summary is never parsed, unknown kinds degrade to
 * generic only where documented. This module additionally records WHICH source
 * produced the snapshot and any unknown-version diagnostic, so the caller can
 * show an honest "classic projection / interface error" state instead of
 * silently painting fabricated data.
 */
import {
  adaptV5Graph,
  adaptV6Snapshot,
  type CanvasSnapshot,
} from "@multica/core/adapters";
import type {
  ResearchGraphEdge,
  ResearchGraphNode,
} from "@multica/core/types";
import type { TypedGraphResponse } from "@multica/core/research";
import type { ResearchV6Snapshot } from "@multica/core/types/research-v6";
import type { ResearchSource } from "./capability";
import {
  canRenderCanvas,
  emptyCanvasSnapshot,
  type CapabilityVerdict,
} from "./capability";

export interface ResearchSessionCanvas {
  /** Which adapter produced this snapshot ("v5" | "v6"). */
  source: ResearchSource;
  /** Unified render-layer snapshot (canonical fields only). */
  snapshot: CanvasSnapshot;
  /** True while no source is available (before probe / empty session). */
  empty: boolean;
  /** Non-empty only when the source degraded to generic / unknown kinds. */
  diagnostics: ReadonlyArray<{ field: string; raw: string; ownerId: string }>;
}

/** A synchronous, error-bearing V5 graph load source. */
export interface V5SessionGraphInput {
  sessionId: string;
  nodes: readonly ResearchGraphNode[];
  edges: readonly ResearchGraphEdge[];
}

/**
 * Build a unified canvas from the V5 session projection.
 * Uses the core `adaptV5Graph`; `v5SnapshotId(sessionId)` gives a stable id.
 */
export function adaptV5Session(
  input: V5SessionGraphInput,
): ResearchSessionCanvas {
  const snapshot = adaptV5Graph(
    input.sessionId,
    input.nodes as ResearchGraphNode[],
    input.edges as ResearchGraphEdge[],
  );
  return {
    source: "v5",
    snapshot,
    empty: snapshot.nodes.length === 0 && snapshot.edges.length === 0,
    diagnostics: [],
  };
}

/**
 * Build a unified canvas from the V6 projection snapshot.
 * Uses the core `adaptV6Snapshot`; unknown kinds degrade to generic there.
 */
export function adaptV6Session(
  snapshot: ResearchV6Snapshot,
): ResearchSessionCanvas {
  const canvas = adaptV6Snapshot(snapshot);
  return {
    source: "v6",
    snapshot: canvas,
    empty: canvas.nodes.length === 0 && canvas.edges.length === 0,
    // Unknown kinds degrade to generic per §7.1; we surface them as diagnostics
    // so a caller can render a generic card + a diagnostic instead of guessing.
    diagnostics: canvas.nodes
      .filter((n) => n.kind === "generic")
      .map((n) => ({
        field: "node_kind",
        raw: String(n.payload?.node_kind ?? n.payload?.entity_kind ?? ""),
        ownerId: n.id,
      })),
  };
}

/**
 * Shape bridge from the canonical projection to the existing D5 renderer.
 * Missing V6 fields stay empty: no tier, confidence, cluster, or lineage is
 * inferred from title/summary text.
 */
export function canvasSnapshotToTypedGraph(
  sessionId: string,
  snapshot: CanvasSnapshot,
): TypedGraphResponse {
  return {
    session_id: sessionId,
    graph_version: snapshot.throughEventSequence,
    total_node_count: snapshot.nodes.length,
    nodes: snapshot.nodes.map((node) => ({
      id: node.id,
      session_id: sessionId,
      node_type: node.kind,
      title: node.title,
      summary: node.summary,
      status: node.status,
      actor_agent_id: node.actor ?? null,
      payload: node.payload,
      level: "",
      round: 0,
      cluster_id: null,
      confidence: null,
      document_count: 0,
      conclusion_count: 0,
      goal_version_id: node.planVersion ?? null,
      derived_from: null,
      merged_from: [],
      superseded_by: null,
      restart_of: null,
      invalidated_by: null,
      superseded_at: null,
      invalidated_at: null,
      parent_id: null,
      child_ids: [],
      children_of: [],
      created_at: node.createdAt ?? "",
      updated_at: node.updatedAt ?? "",
    })),
    edges: snapshot.edges.map((edge) => ({
      id: edge.id,
      session_id: sessionId,
      from_node_id: edge.from,
      to_node_id: edge.to,
      edge_type: edge.relation,
      created_at: typeof edge.createdAt === "string" ? edge.createdAt : "",
    })),
    clusters: [],
    lineage: {
      derived: {},
      merged: {},
      superseded: {},
      restarted: {},
      invalidated: {},
      supersedes: {},
    },
  };
}

/**
 * Resolve which adapter produced a renderable canvas, honouring the verdict:
 *   - `fallback-v5` / `v6` → build the unified canvas from the matching source;
 *   - `interface-error` / `unknown-version` → return an explicit error state,
 *     never silently fall back to V5 and paint a different graph.
 */
export type AdapterResolution =
  | { state: "ok"; canvas: ResearchSessionCanvas }
  | { state: "error"; kind: "interface-error" | "unknown-version"; reason: string };

export function resolveSessionCanvas(args: {
  verdict: CapabilityVerdict;
  v5?: V5SessionGraphInput;
  v6?: ResearchV6Snapshot;
}): AdapterResolution {
  const { verdict, v5, v6 } = args;

  if (!canRenderCanvas(verdict)) {
    return { state: "error", kind: verdict.kind, reason: verdict.reason };
  }

  if (verdict.kind === "v6") {
    if (!v6) {
      return {
        state: "error",
        kind: "interface-error",
        reason: "V6 selected but no V6 snapshot was produced",
      };
    }
    return { state: "ok", canvas: adaptV6Session(v6) };
  }

  // fallback-v5
  if (!v5) {
    return {
      state: "error",
      kind: "interface-error",
      reason: "V5 fallback selected but no V5 session graph was available",
    };
  }
  return { state: "ok", canvas: adaptV5Session(v5) };
}

export { emptyCanvasSnapshot };
