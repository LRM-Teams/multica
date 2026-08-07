import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";
import {
  createWorkspaceAwareStorage,
  registerForWorkspaceRehydration,
} from "../platform/workspace-storage";
import { defaultStorage } from "../platform/storage";

/**
 * LRM-1497 — shared client-side state for the research star-map canvas.
 *
 * Viewport (world x/y + zoom), selected node, and display-only filter belong to
 * the *client* (they are never canonical graph data), so they live here in a
 * `packages/core` Zustand store rather than in server/React-Query state. The
 * store is renderer-agnostic: the `camera` module holds the imperative
 * controller (LRM-1467) while this store is the single source of truth for the
 * shared values a canvas plugin / dock / status bar / detail panel can read.
 *
 * Canonical graph nodes keep flowing through React Query; `filter` only changes
 * what is *shown*, and `visibleCount`/`hiddenCount` are pure helpers over the
 * canonical input (never mutating it).
 */

export interface CanvasViewport {
  x: number;
  y: number;
  zoom: number;
}

/** Display-only filter over the canonical node set (LRM-1497 AC: filter only
 *  changes display, never the canonical graph). */
export interface ResearchCanvasFilter {
  /** Node lifecycle/status filter, when set. */
  status?: string | null;
  /** Node level/tier filter, when set. */
  tier?: string | null;
  /** Round/iteration filter, when set. */
  round?: string | null;
  /** Topic-cluster filter, when set. */
  cluster?: string | null;
  /** Free-text keyword (title / agent / conclusion / evidence). */
  query?: string;
}

export function emptyCanvasFilter(): ResearchCanvasFilter {
  return { status: null, tier: null, round: null, cluster: null, query: "" };
}

export function isBlankFilter(filter: ResearchCanvasFilter): boolean {
  return (
    !filter.status &&
    !filter.tier &&
    !filter.round &&
    !filter.cluster &&
    !(filter.query && filter.query.trim())
  );
}

type CanvasState = {
  /** World-space viewport; `null` when never set (canvas may fitView itself). */
  viewport: CanvasViewport | null;
  /** Currently selected node id (client selection, not canonical). */
  selectedNodeId: string | null;
  /** Current display-only filter. */
  filter: ResearchCanvasFilter;
  setViewport: (viewport: CanvasViewport) => void;
  clearViewport: () => void;
  selectNode: (nodeId: string | null) => void;
  clearSelection: () => void;
  setFilter: (filter: Partial<ResearchCanvasFilter>) => void;
  clearFilter: () => void;
};

export const useResearchCanvasStore = create<CanvasState>()(
  persist(
    (set) => ({
      viewport: null,
      selectedNodeId: null,
      filter: emptyCanvasFilter(),
      setViewport: (viewport) => set({ viewport }),
      clearViewport: () => set({ viewport: null }),
      selectNode: (nodeId) => set({ selectedNodeId: nodeId }),
      clearSelection: () => set({ selectedNodeId: null }),
      setFilter: (filter) =>
        set((s) => ({ filter: { ...s.filter, ...filter } })),
      clearFilter: () => set({ filter: emptyCanvasFilter() }),
    }),
    {
      name: "multica_research_canvas_v1",
      storage: createJSONStorage(() =>
        createWorkspaceAwareStorage(defaultStorage),
      ),
      partialize: (s) => ({
        viewport: s.viewport,
        selectedNodeId: s.selectedNodeId,
        filter: s.filter,
      }),
    },
  ),
);

registerForWorkspaceRehydration(() =>
  useResearchCanvasStore.persist.rehydrate(),
);

/**
 * Pure display-count helper over the canonical node set. Applied to a list of
 * canonical nodes/edges it returns how many are hidden by the current filter.
 * It never mutates the input. A blank filter hides nothing.
 */
export function countHiddenByFilter(
  nodes: ReadonlyArray<{ id: string; node_type?: string; status?: string | null; round?: string | null; cluster?: string | null; title?: string; agent?: string | null; conclusion?: string | null; evidence?: unknown }>,
  filter: ResearchCanvasFilter,
): { visible: number; hidden: number } {
  if (isBlankFilter(filter)) {
    return { visible: nodes.length, hidden: 0 };
  }
  const q = (filter.query || "").trim().toLowerCase();
  let hidden = 0;
  for (const n of nodes) {
    let match = true;
    if (filter.status && n.status !== filter.status) match = false;
    if (match && filter.tier && n.node_type !== filter.tier) match = false;
    if (match && filter.round && n.round !== filter.round) match = false;
    if (match && filter.cluster && n.cluster !== filter.cluster) match = false;
    if (match && q) {
      const haystack = [n.title, n.agent, n.conclusion, stringifyEvidence(n.evidence)]
        .filter(Boolean)
        .join(" ")
        .toLowerCase();
      if (!haystack.includes(q)) match = false;
    }
    if (!match) hidden += 1;
  }
  return { visible: nodes.length - hidden, hidden };
}

function stringifyEvidence(evidence: unknown): string {
  if (evidence == null) return "";
  try {
    return typeof evidence === "string" ? evidence : JSON.stringify(evidence);
  } catch {
    return "";
  }
}
