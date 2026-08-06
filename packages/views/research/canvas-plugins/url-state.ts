"use client";

/**
 * FE-10 / LRM-1486 — canvas URL state adapter (pure core).
 *
 * Only `lens`, `node`, and `view` belong in the URL (deep-linkable). Canvas
 * coordinates, temporary folds and the motion queue deliberately do NOT
 * pollute the URL. Integration is framework-agnostic — History API only, zero
 * `next/*` / `react-router-dom`, so Web and Desktop share the same adapter.
 *
 * All functions are pure over `URLSearchParams`-like inputs so the adapter is
 * unit-testable without a browser.
 */

export const CANVAS_URL_PARAMS = ["lens", "node", "view"] as const;

export type CanvasUrlParam = (typeof CANVAS_URL_PARAMS)[number];

/** Deep-linkable canvas state — the ONLY fields allowed in the URL. */
export interface CanvasUrlState {
  /** Active lens (执行 / 探索 / 证据 / Insight / 争议). */
  lens?: string;
  /** Selected node id (drives focus + Inspector). */
  node?: string;
  /** Independent view (e.g. the Git trajectory explorer). */
  view?: string;
}

export const EMPTY_CANVAS_URL_STATE: CanvasUrlState = {};

function firstParam(
  params: URLSearchParams,
  key: string,
): string | undefined {
  const v = params.get(key);
  return v && v.length > 0 ? v : undefined;
}

/**
 * Read the three deep-linkable params from a query string. Unknown params are
 * ignored and never interpreted as canvas state. Returns the empty state when
 * no canvas param is present.
 */
export function parseCanvasUrlState(search: string): CanvasUrlState {
  const params = new URLSearchParams(search.startsWith("?") ? search.slice(1) : search);
  const state: CanvasUrlState = {};
  const lens = firstParam(params, "lens");
  const node = firstParam(params, "node");
  const view = firstParam(params, "view");
  if (lens) state.lens = lens;
  if (node) state.node = node;
  if (view) state.view = view;
  return state;
}

/**
 * Serialize canvas state into a query string, PRESERVING any unrelated query
 * params already in `existingSearch` and only ever writing `lens/node/view`.
 * Viewport/fold/motion params (even if present) are never emitted.
 *
 * Returns a string WITHOUT the leading `?` (suitable for pushState/replaceState
 * `?` + value).
 */
export function serializeCanvasUrlState(
  state: CanvasUrlState,
  existingSearch = "",
): string {
  const params = new URLSearchParams(
    existingSearch.startsWith("?") ? existingSearch.slice(1) : existingSearch,
  );
  // Remove any stale canvas params so cleared fields drop out of the URL.
  for (const key of CANVAS_URL_PARAMS) params.delete(key);
  for (const key of CANVAS_URL_PARAMS) {
    const value = state[key];
    if (value && value.length > 0) params.set(key, value);
  }
  const out = params.toString();
  return out;
}

/**
 * True when a query string differs from `state` in at least one canvas param
 * — used to avoid redundant history pushes.
 */
export function canvasUrlStateEquals(
  state: CanvasUrlState,
  search: string,
): boolean {
  const current = parseCanvasUrlState(search);
  return (
    (state.lens ?? undefined) === (current.lens ?? undefined) &&
    (state.node ?? undefined) === (current.node ?? undefined) &&
    (state.view ?? undefined) === (current.view ?? undefined)
  );
}
