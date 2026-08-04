import type { AggregateTreeSelection } from "./aggregate-tree";

export type AggregateTreeParentIndex = Readonly<Record<string, string | null>>;

export type AggregateTreeSessionNavigation = {
  focusPath: readonly string[];
  selectedNodeId: string | null;
};

export type AggregateTreeNavigationState = {
  sessions: Readonly<Record<string, AggregateTreeSessionNavigation>>;
};

export type AggregateTreeNavigationAction =
  | { type: "open"; sessionId: string; path: readonly string[] }
  | { type: "select"; sessionId: string; nodeId: string | null }
  | { type: "collapse"; sessionId: string; nodeId: string }
  | { type: "jumpToAncestor"; sessionId: string; nodeId: string }
  | { type: "replaceTree"; sessionId: string; parentById: AggregateTreeParentIndex };

export type AggregateTreeNavigationSelection = {
  /** The single expanded root-to-focus path used by the canvas. */
  expandedNodeIds: readonly string[];
  /** Node ids for breadcrumb rendering, ordered root first. */
  breadcrumbs: readonly string[];
  /** Camera target after an open, collapse, or ancestor jump. */
  focusNodeId: string | null;
  selectedNodeId: string | null;
};

const EMPTY_SESSION: AggregateTreeSessionNavigation = {
  focusPath: [],
  selectedNodeId: null,
};

export const initialAggregateTreeNavigationState: AggregateTreeNavigationState = {
  sessions: {},
};

function hasNode(index: AggregateTreeParentIndex, nodeId: string): boolean {
  return Object.prototype.hasOwnProperty.call(index, nodeId);
}

function normalizePath(path: readonly string[]): readonly string[] {
  const seen = new Set<string>();
  const normalized: string[] = [];
  for (const nodeId of path) {
    if (nodeId.length === 0 || seen.has(nodeId)) break;
    seen.add(nodeId);
    normalized.push(nodeId);
  }
  return normalized;
}

function validPathPrefix(
  path: readonly string[],
  parentById: AggregateTreeParentIndex,
): readonly string[] {
  const valid: string[] = [];
  for (const nodeId of path) {
    if (!hasNode(parentById, nodeId)) break;
    const expectedParentId = valid.at(-1) ?? null;
    if (parentById[nodeId] !== expectedParentId) break;
    valid.push(nodeId);
  }
  return valid;
}

function isVisibleSelection(
  nodeId: string,
  focusPath: readonly string[],
  parentById: AggregateTreeParentIndex,
): boolean {
  const parentId = parentById[nodeId];
  return (
    focusPath.includes(nodeId) ||
    (parentId === null
      ? focusPath.length === 0
      : parentId !== undefined && focusPath.includes(parentId))
  );
}

function updateSession(
  state: AggregateTreeNavigationState,
  sessionId: string,
  update: (session: AggregateTreeSessionNavigation) => AggregateTreeSessionNavigation | null,
): AggregateTreeNavigationState {
  const current = state.sessions[sessionId] ?? EMPTY_SESSION;
  const next = update(current);
  if (next === null || next === current) return state;
  return {
    sessions: {
      ...state.sessions,
      [sessionId]: next,
    },
  };
}

/**
 * Pure navigation state machine. It retains node ids only; server graph objects
 * stay in React Query and are supplied transiently to replaceTree as an index.
 */
export function aggregateTreeNavigationReducer(
  state: AggregateTreeNavigationState,
  action: AggregateTreeNavigationAction,
): AggregateTreeNavigationState {
  switch (action.type) {
    case "open":
      return updateSession(state, action.sessionId, () => {
        const focusPath = normalizePath(action.path);
        return {
          focusPath,
          selectedNodeId: focusPath.at(-1) ?? null,
        };
      });
    case "select":
      return updateSession(state, action.sessionId, (session) =>
        session.selectedNodeId === action.nodeId
          ? null
          : { ...session, selectedNodeId: action.nodeId },
      );
    case "collapse":
      return updateSession(state, action.sessionId, (session) => {
        const index = session.focusPath.indexOf(action.nodeId);
        if (index < 0) return null;
        const focusPath = session.focusPath.slice(0, index);
        return {
          focusPath,
          selectedNodeId: focusPath.at(-1) ?? null,
        };
      });
    case "jumpToAncestor":
      return updateSession(state, action.sessionId, (session) => {
        const index = session.focusPath.indexOf(action.nodeId);
        if (index < 0) return null;
        return {
          focusPath: session.focusPath.slice(0, index + 1),
          selectedNodeId: action.nodeId,
        };
      });
    case "replaceTree":
      return updateSession(state, action.sessionId, (session) => {
        const focusPath = validPathPrefix(session.focusPath, action.parentById);
        const selectedNodeId =
          session.selectedNodeId !== null &&
          hasNode(action.parentById, session.selectedNodeId) &&
          isVisibleSelection(session.selectedNodeId, focusPath, action.parentById)
            ? session.selectedNodeId
            : (focusPath.at(-1) ?? null);
        return { focusPath, selectedNodeId };
      });
  }
}

/** Builds the transient structural index consumed by replaceTree. */
export function aggregateTreeParentIndex(
  tree: AggregateTreeSelection,
): AggregateTreeParentIndex {
  if (tree.status !== "ready") return {};
  return Object.fromEntries(
    [...tree.byId.values()].map((entry) => [entry.id, entry.parentId]),
  );
}

/** Resolves a canonical root-to-node path without retaining the tree in state. */
export function aggregateTreePathToNode(
  parentById: AggregateTreeParentIndex,
  nodeId: string,
): readonly string[] {
  const reversePath: string[] = [];
  const seen = new Set<string>();
  let currentId: string | null = nodeId;
  while (currentId !== null && hasNode(parentById, currentId) && !seen.has(currentId)) {
    seen.add(currentId);
    reversePath.push(currentId);
    currentId = parentById[currentId] ?? null;
  }
  if (currentId !== null) return [];
  return reversePath.reverse();
}

export function selectAggregateTreeNavigation(
  state: AggregateTreeNavigationState,
  sessionId: string,
): AggregateTreeNavigationSelection {
  const session = state.sessions[sessionId] ?? EMPTY_SESSION;
  return {
    expandedNodeIds: session.focusPath,
    breadcrumbs: session.focusPath,
    focusNodeId: session.focusPath.at(-1) ?? null,
    selectedNodeId: session.selectedNodeId,
  };
}
