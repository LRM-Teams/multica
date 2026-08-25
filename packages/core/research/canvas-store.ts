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

const MAX_SESSION_VIEWPORTS = 20;
const MAX_SESSION_SELECTIONS = 20;
const MAX_SESSION_FILTERS = 20;

function retainRecentSessionViewports(
  current: Record<string, CanvasViewport>,
  sessionId: string,
  viewport: CanvasViewport,
): Record<string, CanvasViewport> {
  const previous = Object.entries(current).filter(([key]) => key !== sessionId);
  return Object.fromEntries([
    ...previous.slice(-(MAX_SESSION_VIEWPORTS - 1)),
    [sessionId, viewport],
  ]);
}

function retainRecentSessionSelections(
  current: Record<string, string>,
  sessionId: string,
  nodeId: string | null,
): Record<string, string> {
  const previous = Object.entries(current).filter(([key]) => key !== sessionId);
  if (nodeId == null) return Object.fromEntries(previous);
  return Object.fromEntries([
    ...previous.slice(-(MAX_SESSION_SELECTIONS - 1)),
    [sessionId, nodeId],
  ]);
}

function retainRecentSessionFilters(
  current: Record<string, ResearchCanvasFilter>,
  sessionId: string,
  filter: ResearchCanvasFilter,
): Record<string, ResearchCanvasFilter> {
  const previous = Object.entries(current).filter(([key]) => key !== sessionId);
  return Object.fromEntries([
    ...previous.slice(-(MAX_SESSION_FILTERS - 1)),
    [sessionId, filter],
  ]);
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
  /** Session-scoped viewports prevent camera state leaking between research runs. */
  viewportBySession: Record<string, CanvasViewport>;
  /** Currently selected node id (client selection, not canonical). */
  selectedNodeId: string | null;
  /** Session-scoped transient selection without leaking across runs. */
  selectedNodeBySession: Record<string, string>;
  /** Current display-only filter. */
  filter: ResearchCanvasFilter;
  /** Session-scoped display filters restore without leaking across runs. */
  filterBySession: Record<string, ResearchCanvasFilter>;
  setViewport: (viewport: CanvasViewport) => void;
  setSessionViewport: (sessionId: string, viewport: CanvasViewport) => void;
  clearViewport: () => void;
  selectNode: (nodeId: string | null) => void;
  selectSessionNode: (sessionId: string, nodeId: string | null) => void;
  clearSelection: () => void;
  setFilter: (filter: Partial<ResearchCanvasFilter>) => void;
  clearFilter: () => void;
  setSessionFilter: (
    sessionId: string,
    filter: Partial<ResearchCanvasFilter>,
  ) => void;
  clearSessionFilter: (sessionId: string) => void;
};

export const useResearchCanvasStore = create<CanvasState>()(
  persist(
    (set) => ({
      viewport: null,
      viewportBySession: {},
      selectedNodeId: null,
      selectedNodeBySession: {},
      filter: emptyCanvasFilter(),
      filterBySession: {},
      setViewport: (viewport) => set({ viewport }),
      setSessionViewport: (sessionId, viewport) =>
        set((state) => ({
          viewportBySession: retainRecentSessionViewports(
            state.viewportBySession,
            sessionId,
            viewport,
          ),
        })),
      clearViewport: () => set({ viewport: null }),
      selectNode: (nodeId) => set({ selectedNodeId: nodeId }),
      selectSessionNode: (sessionId, nodeId) =>
        set((state) => ({
          selectedNodeBySession: retainRecentSessionSelections(
            state.selectedNodeBySession,
            sessionId,
            nodeId,
          ),
        })),
      clearSelection: () => set({ selectedNodeId: null }),
      setFilter: (filter) =>
        set((s) => ({ filter: { ...s.filter, ...filter } })),
      clearFilter: () => set({ filter: emptyCanvasFilter() }),
      setSessionFilter: (sessionId, filter) =>
        set((state) => ({
          filterBySession: retainRecentSessionFilters(
            state.filterBySession,
            sessionId,
            {
              ...(state.filterBySession[sessionId] ?? emptyCanvasFilter()),
              ...filter,
            },
          ),
        })),
      clearSessionFilter: (sessionId) =>
        set((state) => ({
          filterBySession: retainRecentSessionFilters(
            state.filterBySession,
            sessionId,
            emptyCanvasFilter(),
          ),
        })),
    }),
    {
      name: "multica_research_canvas_v1",
      storage: createJSONStorage(() =>
        createWorkspaceAwareStorage(defaultStorage),
      ),
      partialize: (s) => ({
        viewport: s.viewport,
        viewportBySession: s.viewportBySession,
        filter: s.filter,
        filterBySession: s.filterBySession,
      }),
    },
  ),
);

registerForWorkspaceRehydration(() =>
  useResearchCanvasStore.persist.rehydrate(),
);

/** Minimal node shape for display-only canvas filtering (typed graph + legacy snapshot). */
export interface ResearchCanvasFilterableNode {
  id: string;
  status?: string | null;
  node_type?: string | null;
  level?: string | null;
  round?: string | number | null;
  cluster_id?: string | null;
  cluster?: string | null;
  title?: string | null;
  summary?: string | null;
  actor_agent_id?: string | null;
  agent?: string | null;
  conclusion?: string | null;
  evidence?: unknown;
}

export function matchesResearchCanvasFilter(
  node: ResearchCanvasFilterableNode,
  filter: ResearchCanvasFilter,
): boolean {
  if (isBlankFilter(filter)) return true;

  if (filter.status && (node.status ?? "") !== filter.status) return false;

  if (filter.tier) {
    const tier = (node.level || node.node_type || "").toLowerCase();
    if (tier !== filter.tier.toLowerCase()) return false;
  }

  if (filter.round) {
    const round = node.round != null && node.round !== "" ? String(node.round) : "";
    if (round !== filter.round) return false;
  }

  const cluster = node.cluster_id ?? node.cluster ?? "";
  if (filter.cluster && cluster !== filter.cluster) return false;

  const q = (filter.query || "").trim().toLowerCase();
  if (q) {
    const haystack = [
      node.title,
      node.summary,
      node.agent,
      node.actor_agent_id,
      node.conclusion,
      stringifyEvidence(node.evidence),
    ]
      .filter(Boolean)
      .join(" ")
      .toLowerCase();
    if (!haystack.includes(q)) return false;
  }

  return true;
}

/**
 * Pure display-count helper over the canonical node set. Applied to a list of
 * canonical nodes/edges it returns how many are hidden by the current filter.
 * It never mutates the input. A blank filter hides nothing.
 */
export function countHiddenByFilter(
  nodes: ReadonlyArray<ResearchCanvasFilterableNode>,
  filter: ResearchCanvasFilter,
): { visible: number; hidden: number } {
  if (isBlankFilter(filter)) {
    return { visible: nodes.length, hidden: 0 };
  }
  let hidden = 0;
  for (const n of nodes) {
    if (!matchesResearchCanvasFilter(n, filter)) hidden += 1;
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
