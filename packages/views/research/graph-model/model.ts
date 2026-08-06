/**
 * Unified canvas ViewModel (FE-04).
 *
 * Single source of truth for the research canvas render layer. Holds:
 *   - the canonical graph (`CanvasSnapshot`) — canonical fields ONLY come from
 *     the projection adapters (§7.1);
 *   - display-only state: hidden (folded) node ids and layout positions — these
 *     are client state and are never written back to the canonical graph;
 *   - an idempotent reducer that applies V5/V6 deltas and visibility
 *     tombstones with a scoped (local-only) layout recompute.
 *
 * Stability guarantees tested by this module:
 *   - `reset(snapshot)` twice on an identical snapshot yields identical node
 *     positions and identity → no meaningless jitter on recompute (AC1).
 *   - Applying a visibility tombstone removes the view node + dangling edges
 *     and only repositions the affected region; every unaffected node keeps
 *     its exact prior position (AC2).
 */
import type { CanvasDelta, CanvasSnapshot } from "@multica/core/adapters";
import { applyCanvasDelta } from "@multica/core/adapters";
import {
  deterministicPositions,
  recomputeScoped,
  type PositionMap,
  type View,
} from "./positioner";
import type { RenderEdge, RenderNode } from "./types";

export interface CanvasModelState {
  snapshot: CanvasSnapshot;
  /** Client-only folding (display state — never canonical). */
  hiddenNodeIds: ReadonlySet<string>;
  /** Client-only layout (display state). */
  positions: PositionMap;
}

export type CanvasAction =
  | { type: "reset"; snapshot: CanvasSnapshot }
  | { type: "delta"; delta: CanvasDelta }
  | { type: "setHidden"; nodeIds: readonly string[] }
  | { type: "clearHidden" };

export const initialCanvasModel = (): CanvasModelState => ({
  snapshot: {
    snapshotId: "",
    throughEventSequence: 0,
    graphContentHash: "",
    nodes: [],
    edges: [],
  },
  hiddenNodeIds: new Set(),
  positions: new Map(),
});

/** Canonical nodes minus display-hidden nodes. */
export function visibleView(state: CanvasModelState): View {
  const nodes = state.snapshot.nodes.filter(
    (n) => !state.hiddenNodeIds.has(n.id),
  );
  const idSet = new Set(nodes.map((n) => n.id));
  const edges = state.snapshot.edges.filter(
    (e) => idSet.has(e.from) && idSet.has(e.to),
  );
  return { nodes, edges };
}

/** Node ids that are new relative to the previous snapshot (need placement). */
function newPlacementIds(
  prev: ReadonlySet<string>,
  view: View,
): string[] {
  const newIds: string[] = [];
  for (const n of view.nodes) {
    if (!prev.has(n.id)) newIds.push(n.id);
  }
  return newIds;
}

function lightNodeIds(state: CanvasModelState): Set<string> {
  return new Set(state.snapshot.nodes.map((n) => n.id));
}

export function canvasModelReducer(
  state: CanvasModelState,
  action: CanvasAction,
): CanvasModelState {
  switch (action.type) {
    case "reset":
      return resetWithSnapshot(action.snapshot);
    case "delta": {
      const prevIds = new Set(state.snapshot.nodes.map((n) => n.id));
      const applied = applyCanvasDelta(state.snapshot, action.delta);
      if (applied.needsResync) {
        // Caller should refetch the snapshot; local state is left untouched.
        return state;
      }
      if (applied.wasDuplicate) return state;

      const newState: CanvasModelState = {
        snapshot: applied.snapshot,
        // Newly appeared nodes should never be auto-hidden.
        hiddenNodeIds: new Set(
          [...state.hiddenNodeIds].filter((id) =>
            applied.snapshot.nodes.some((n) => n.id === id),
          ),
        ),
        positions: state.positions,
      };

      // Prune positions for removed/never-again nodes.
      const present = new Set(applied.snapshot.nodes.map((n) => n.id));
      const pruned = new Map<string, { x: number; y: number }>();
      for (const [id, p] of state.positions) {
        if (present.has(id)) pruned.set(id, p);
      }
      newState.positions = pruned;

      const view = visibleView(newState);
      const newIds = newPlacementIds(prevIds, view);
      const affected =
        applied.affectedRootIds.length > 0
          ? applied.affectedRootIds
          : newIds;
      newState.positions = recomputeScoped(
        pruned,
        view,
        affected,
        newIds,
      );
      return newState;
    }
    case "setHidden": {
      const prev = lightNodeIds(state);
      const hidden = new Set(action.nodeIds);
      const view = visibleView({ ...state, hiddenNodeIds: hidden });
      const newIds = newPlacementIds(prev, view);
      // Folding never places hidden nodes; only reveal may add them.
      const affectedRoots = [...hidden].filter((id) => prev.has(id));
      return {
        ...state,
        hiddenNodeIds: hidden,
        positions: recomputeScoped(
          state.positions,
          view,
          affectedRoots,
          newIds,
        ),
      };
    }
    case "clearHidden": {
      const view = visibleView({ ...state, hiddenNodeIds: new Set() });
      const newIds = newPlacementIds(lightNodeIds(state), view);
      return {
        ...state,
        hiddenNodeIds: new Set(),
        positions: recomputeScoped(state.positions, view, [], newIds),
      };
    }
    default:
      return state;
  }
}

/** Rebuild the whole model from a fresh snapshot (first paint / resync). */
export function resetWithSnapshot(
  snapshot: CanvasSnapshot,
): CanvasModelState {
  const state: CanvasModelState = {
    snapshot,
    hiddenNodeIds: new Set(),
    positions: deterministicPositions({ nodes: snapshot.nodes, edges: snapshot.edges }),
  };
  return state;
}

/** Render-layer projection: canonical + geometry, dangling edges dropped. */
export function renderCanvas(state: CanvasModelState): {
  nodes: RenderNode[];
  edges: RenderEdge[];
} {
  const view = visibleView(state);
  const positions = state.positions;
  const nodes: RenderNode[] = view.nodes.map((n) => ({
    id: n.id,
    kind: n.kind,
    title: n.title,
    status: n.status,
    importance: n.importance,
    freshness: n.freshness,
    position: positions.get(n.id) ?? { x: 0, y: 0 },
  }));
  const edges: RenderEdge[] = view.edges.map((e) => ({
    id: e.id,
    from: e.from,
    to: e.to,
    relation: e.relation,
  }));
  return { nodes, edges };
}
