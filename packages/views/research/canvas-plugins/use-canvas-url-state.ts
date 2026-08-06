"use client";

import { useCallback, useMemo, useSyncExternalStore } from "react";
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
  return {
    getSearch: () => window.location.search,
    getPathname: () => window.location.pathname,
    push: (search, title) => {
      window.history.pushState(null, title, `${window.location.pathname}${search || ""}`);
    },
    replace: (search, title) => {
      window.history.replaceState(null, title, `${window.location.pathname}${search || ""}`);
    },
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

/** Render-safe no-op fallback so the hook never returns before calling hooks. */
const NOOP_HISTORY: CanvasUrlHistory = {
  getSearch: () => "",
  getPathname: () => "/",
  push: () => {},
  replace: () => {},
  subscribe: () => () => {},
  notify: () => {},
};

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
  const history = options.history ?? DEFAULT_HISTORY ?? NOOP_HISTORY;

  // The URL is the external store; a raw search string is a naturally stable
  // snapshot for useSyncExternalStore (no render-phase ref mutation needed).
  const subscribe = useCallback(
    (listener: () => void) => history.subscribe(listener),
    [history],
  );
  const getSnapshot = useCallback(() => history.getSearch(), [history]);
  const search = useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
  const state = useMemo(() => parseCanvasUrlState(search), [search]);

  const set = useCallback(
    (patch: CanvasUrlState) => {
      const raw = history.getSearch();
      const current = parseCanvasUrlState(raw);
      // `undefined` keeps the current value; empty string clears it. This way
      // changing only `node` never wipes `lens`/`view`, and clearing is
      // explicit (serialize drops empty fields from the URL, so the store
      // re-read yields `undefined` for cleared ones).
      const next: CanvasUrlState = {
        lens: patch.lens === undefined ? current.lens : patch.lens,
        node: patch.node === undefined ? current.node : patch.node,
        view: patch.view === undefined ? current.view : patch.view,
      };
      const serialized = serializeCanvasUrlState(next, raw);
      const wantsUrl = serialized === "" ? "" : `?${serialized}`;
      // Avoid redundant history entries when nothing changed.
      if (!canvasUrlStateEquals(next, raw)) {
        history.push(wantsUrl, "Research canvas");
        // pushState does not fire `popstate`; manually notify so the store
        // re-reads the new URL.
        history.notify();
      }
    },
    [history],
  );

  return { state, set };
}
