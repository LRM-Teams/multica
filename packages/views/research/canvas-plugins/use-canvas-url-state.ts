"use client";

import { useCallback, useEffect, useState } from "react";
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
}

export function createWindowCanvasUrlHistory(): CanvasUrlHistory {
  const push = (search: string, title: string) => {
    window.history.pushState(null, title, `${window.location.pathname}${search || ""}`);
  };
  const replace = (search: string, title: string) => {
    window.history.replaceState(null, title, `${window.location.pathname}${search || ""}`);
  };
  return {
    getSearch: () => window.location.search,
    getPathname: () => window.location.pathname,
    push,
    replace,
    subscribe: (listener) => {
      window.addEventListener("popstate", listener);
      return () => window.removeEventListener("popstate", listener);
    },
  };
}

export interface UseCanvasUrlStateOptions {
  history?: CanvasUrlHistory;
  /** Default when a listener replaces state — re-reading keeps it the source of truth. */
}

/**
 * FE-10 / LRM-1486 — URL state adapter for deep-linking lens/node/view.
 *
 * Reads initial state from the URL (deep-link), exposes a stable `set()`, and
 * follows browser Back/Forward via `popstate`. It ONLY ever writes
 * lens/node/view to the query string — viewport/fold/motion state is kept out
 * of the URL entirely (see `url-state.ts`).
 *
 * Zero router dependencies: Web and Desktop share this via the History API.
 */
export function useCanvasUrlState(
  options: UseCanvasUrlStateOptions = {},
): { state: CanvasUrlState; set: (patch: CanvasUrlState) => void } {
  const history = options.history ?? createWindowCanvasUrlHistory();
  const [state, setState] = useState<CanvasUrlState>(() =>
    parseCanvasUrlState(history.getSearch()),
  );

  useEffect(() => {
    const unsubscribe = history.subscribe(() => {
      // Back/Forward: re-read the URL as the source of truth.
      setState(parseCanvasUrlState(history.getSearch()));
    });
    return unsubscribe;
    // history instance is stable (module-singleton default or injected once).
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const set = useCallback(
    (patch: CanvasUrlState) => {
      // `undefined` keeps the current value; empty string clears it. This way
      // changing only `node` never wipes `lens`/`view`, and clearing is explicit.
      // `undefined` keeps the current value; empty string clears it. This way
      // changing only `node` never wipes `lens`/`view`, and clearing is explicit.
      const next: CanvasUrlState = {
        lens: patch.lens === undefined ? state.lens : patch.lens,
        node: patch.node === undefined ? state.node : patch.node,
        view: patch.view === undefined ? state.view : patch.view,
      };
      // Normalize empty-string (cleared) fields back to `undefined` in the
      // in-memory state while serialize still drops them from the URL.
      const normalized: CanvasUrlState = {
        lens: next.lens ? next.lens : undefined,
        node: next.node ? next.node : undefined,
        view: next.view ? next.view : undefined,
      };
      const search = serializeCanvasUrlState(next, history.getSearch());
      const wantsUrl = search === "" ? "" : `?${search}`;
      // Avoid redundant history entries when nothing changed.
      if (!canvasUrlStateEquals(next, history.getSearch())) {
        history.push(wantsUrl, "Research canvas");
      }
      setState(normalized);
    },
    [history, state],
  );

  return { state, set };
}
