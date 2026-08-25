import { create } from "zustand";

export interface ResearchV6DirectorExpansionDisplay {
  /**
   * Owning immutable Projection Snapshot; retained until the replacement
   * slice commits.
   */
  snapshotId: string;
  sliceKey: string;
  revealedNodeIds: readonly string[];
}

export interface ResearchV6DirectorExpansionTransition {
  sequence: number;
  kind: "expand" | "collapse";
  rootNodeId: string;
  revealedNodeIds: readonly string[];
}

interface ResearchV6DirectorDisplayState {
  scope: string | null;
  identity: string | null;
  selectedNodeId: string | null;
  expandedByRoot: Record<string, ResearchV6DirectorExpansionDisplay>;
  requestTokenByRoot: Record<string, string>;
  failureByRoot: Record<string, string>;
  transition: ResearchV6DirectorExpansionTransition | null;
  nextTransitionSequence: number;
  setProjectionIdentity: (
    workspaceId: string,
    runId: string,
    snapshotId: string,
  ) => void;
  selectNode: (nodeId: string | null) => void;
  beginExpansion: (rootNodeId: string, requestToken: string) => void;
  commitExpansion: (
    rootNodeId: string,
    requestToken: string,
    snapshotId: string,
    sliceKey: string,
    revealedNodeIds: readonly string[],
    transitionNodeIds?: readonly string[],
  ) => void;
  failExpansion: (
    rootNodeId: string,
    requestToken: string,
    message: string,
  ) => void;
  collapseRoot: (rootNodeId: string) => void;
  invalidateSliceKeys: (sliceKeys: readonly string[]) => void;
  clear: () => void;
}

export function researchV6DirectorDisplayIdentity(
  workspaceId: string,
  runId: string,
  snapshotId: string,
): string {
  return `${workspaceId}:${runId}:${snapshotId}`;
}

export function researchV6DirectorDisplayScope(
  workspaceId: string,
  runId: string,
): string {
  return `${workspaceId}:${runId}`;
}

const EMPTY_DISPLAY = {
  scope: null,
  identity: null,
  selectedNodeId: null,
  expandedByRoot: {},
  requestTokenByRoot: {},
  failureByRoot: {},
  transition: null,
  nextTransitionSequence: 1,
} as const;

/** Presentation state only; server Projection remains in React Query. */
export const useResearchV6DirectorDisplayStore =
  create<ResearchV6DirectorDisplayState>()((set) => ({
    ...EMPTY_DISPLAY,

    setProjectionIdentity(workspaceId, runId, snapshotId) {
      const scope = researchV6DirectorDisplayScope(workspaceId, runId);
      const identity = researchV6DirectorDisplayIdentity(
        workspaceId,
        runId,
        snapshotId,
      );
      set((state) =>
        state.identity === identity
          ? state
          : {
              ...EMPTY_DISPLAY,
              scope,
              identity,
              // Expansion is a Run-scoped user intent. Keep the last confirmed
              // slice visible while its replacement loads for the new Snapshot.
              expandedByRoot:
                state.scope === scope ? state.expandedByRoot : {},
            },
      );
    },

    selectNode(selectedNodeId) {
      set({ selectedNodeId });
    },

    beginExpansion(rootNodeId, requestToken) {
      set((state) => ({
        requestTokenByRoot: {
          ...state.requestTokenByRoot,
          [rootNodeId]: requestToken,
        },
        failureByRoot: withoutKey(state.failureByRoot, rootNodeId),
      }));
    },

    commitExpansion(
      rootNodeId,
      requestToken,
      snapshotId,
      sliceKey,
      revealedNodeIds,
      transitionNodeIds,
    ) {
      set((state) => {
        if (state.requestTokenByRoot[rootNodeId] !== requestToken) return state;
        const revealed = [...new Set(revealedNodeIds)].filter(
          (nodeId) => nodeId !== rootNodeId,
        );
        const animated = [...new Set(transitionNodeIds ?? revealed)].filter(
          (nodeId) => nodeId !== rootNodeId,
        );
        return {
          expandedByRoot: {
            ...state.expandedByRoot,
            [rootNodeId]: {
              snapshotId,
              sliceKey,
              revealedNodeIds: revealed,
            },
          },
          requestTokenByRoot: withoutKey(
            state.requestTokenByRoot,
            rootNodeId,
          ),
          failureByRoot: withoutKey(state.failureByRoot, rootNodeId),
          transition: {
            sequence: state.nextTransitionSequence,
            kind: "expand",
            rootNodeId,
            revealedNodeIds: animated,
          },
          nextTransitionSequence: state.nextTransitionSequence + 1,
        };
      });
    },

    failExpansion(rootNodeId, requestToken, message) {
      set((state) => {
        if (state.requestTokenByRoot[rootNodeId] !== requestToken) return state;
        return {
          requestTokenByRoot: withoutKey(
            state.requestTokenByRoot,
            rootNodeId,
          ),
          failureByRoot: {
            ...state.failureByRoot,
            [rootNodeId]: message,
          },
        };
      });
    },

    collapseRoot(rootNodeId) {
      set((state) => {
        const expanded = state.expandedByRoot[rootNodeId];
        if (!expanded) {
          return {
            requestTokenByRoot: withoutKey(
              state.requestTokenByRoot,
              rootNodeId,
            ),
            failureByRoot: withoutKey(state.failureByRoot, rootNodeId),
          };
        }
        return {
          expandedByRoot: withoutKey(state.expandedByRoot, rootNodeId),
          requestTokenByRoot: withoutKey(
            state.requestTokenByRoot,
            rootNodeId,
          ),
          failureByRoot: withoutKey(state.failureByRoot, rootNodeId),
          transition: {
            sequence: state.nextTransitionSequence,
            kind: "collapse",
            rootNodeId,
            revealedNodeIds: expanded.revealedNodeIds,
          },
          nextTransitionSequence: state.nextTransitionSequence + 1,
        };
      });
    },

    invalidateSliceKeys(sliceKeys) {
      const invalidated = new Set(sliceKeys);
      if (invalidated.size === 0) return;
      set((state) => {
        let expandedByRoot = state.expandedByRoot;
        let transition = state.transition;
        let sequence = state.nextTransitionSequence;
        for (const [rootNodeId, expanded] of Object.entries(
          state.expandedByRoot,
        )) {
          if (!invalidated.has(expanded.sliceKey)) continue;
          expandedByRoot = withoutKey(expandedByRoot, rootNodeId);
          transition = {
            sequence,
            kind: "collapse",
            rootNodeId,
            revealedNodeIds: expanded.revealedNodeIds,
          };
          sequence += 1;
        }
        return {
          expandedByRoot,
          transition,
          nextTransitionSequence: sequence,
        };
      });
    },

    clear() {
      set(EMPTY_DISPLAY);
    },
  }));

function withoutKey<T>(record: Record<string, T>, key: string): Record<string, T> {
  if (!(key in record)) return record;
  const next = { ...record };
  delete next[key];
  return next;
}
