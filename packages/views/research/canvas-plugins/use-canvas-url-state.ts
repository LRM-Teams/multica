"use client";

import { useCallback, useRef, useSyncExternalStore } from "react";
import {
  canvasUrlStateEquals,
  parseCanvasUrlState,
  serializeCanvasUrlState,
  type CanvasUrlState,
} from "./url-state";

/**
 * Minimal History API surface used by the adapter. Injected so tests can drive
 * it with a fake and the Web/Desktop hosts can share the same implementation.
 */
export interface CanvasUrlHistory {
  getSearch(): string;
  getPathname(): string;
  push(search: string, title: string): void;
  replace(search: string, title: string): void;
  subscribe(listener: () => void): () => void;
  /** Notify subscribers after a programmatic push/replace. */
  notify(): void;
}

function createWindowCanvasUrlHistory(): CanvasUrlHistory {
  const listeners = new Set<() => void>();
  const dispatch = () => {
    for (const listener of [...listeners]) listener();
  };
  const push = (search: string, title: string) => {
    window.history.pushState(null, title, `${window.location.pathname}${search || ""}`);
  };
  const replace = (search: string, title: string) => {
    window.history.replaceState(null, title, `${window.location.pathname}${search || ""}`);
  };
  // Real browser Back/Forward fires `popstate`; subscribe bridges it to our
  // listener set so `useSyncExternalStore` sees external URL changes.
  window.addEventListener("popstate", dispatch);
  return {
    getSearch: () => window.location.search,
    getPathname: () => window.location.pathname,
    push,
    replace,
    subscribe: (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    notify: dispatch,
  };
}

/** Shared default so the history instance (and its window listener) is stable. */
const DEFAULT_HISTORY: CanvasUrlHistory | null =
  typeof window === "undefined" ? null : createWindowCanvasUrlHistory();

export interface UseCanvasUrlStateOptions {
  history?: CanvasUrlHistory;
}

/**
 * FE-10 / LRM-1486 — URL state adapter for deep-linking lens/node/view.
 *
 * Reads initial state from the URL (deep-link), exposes a stable `set()`, and
 * follows browser Back/Forward via `useSyncExternalStore` + `popstate`. It
 * ONLY ever writes lens/node/view to the query string — viewport/fold/motion
 * state is kept out of the URL entirely (see `url-state.ts`).
 *
 * Zero router dependencies: Web and Desktop share this via the History API.
 */
export function useCanvasUrlState(
  options: UseCanvasUrlStateOptions = {},
): { state: CanvasUrlState; set: (patch: CanvasUrlState) => void } {
  const historyRef = useRef<CanvasUrlHistory | null>(null);
  if (historyRef.current === null) {
    historyRef.current = options.history ?? DEFAULT_HISTORY!;
  }
  const history = historyRef.current;

  // `useSyncExternalStore` requires a stable snapshot reference unless the
  // underlying store actually changed, so memoize the parsed state keyed by
  // the raw query string.
  const snapshotCacheRef = useRef<{ search: string; state: CanvasUrlState } | null>(null);
  const getSnapshot = useCallback((): CanvasUrlState => {
    const search = history.getSearch();
    const cached = snapshotCacheRef.current;
    if (cached && cached.search === search) return cached.state;
    const state = parseCanvasUrlState(search);
    snapshotCacheRef.current = { search, state };
    return state;
    // history ref is stable for the hook instance.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const subscribe = useCallback(
    (listener: () => void) => history.subscribe(listener),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [],
  );

  const state = useSyncExternalStore(subscribe, getSnapshot, getSnapshot);

  const set = useCallback(
    (patch: CanvasUrlState) => {
      const search = history.getSearch();
      const current = parseCanvasUrlState(search);
      // `undefined` keeps the current value; empty string clears it. This way
      // changing only `node` never wipes `lens`/`view`, and clearing is explicit.
      const next: CanvasUrlState = {
        lens: patch.lens === undefined ? current.lens : patch.lens,
        node: patch.node === undefined ? current.node : patch.node,
        view: patch.view === undefined ? current.view : patch.view,
      };
      // Normalize-empty-string semantics are handled by `serialize` (empty
      // fields are dropped from the URL), so the store re-read yields
      // `undefined` for cleared fields.
      const serialized = serializeCanvasUrlState(next, search);
      const wantsUrl = serialized === "" ? "" : `?${serialized}`;
      // Avoid redundant history entries when nothing changed.
      if (!canvasUrlStateEquals(next, search)) {
        history.push(wantsUrl, "Research canvas");
        // pushState does not fire `popstate`; manually notify so the store
        // re-reads the new URL.
        history.notify();
      }
    },
    // history ref is stable for the hook instance.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [],
  );

  return { state, set };
}
