"use client";

import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";
import { defaultStorage } from "@multica/core/platform";
import type { WorkspaceSearchScope } from "@multica/core/types";

const MAX_RECENT_QUERIES = 8;

interface GlobalSearchState {
  open: boolean;
  scope: WorkspaceSearchScope;
  /** Recent search queries, namespaced by workspace id (see RecentIssuesStore). */
  recentByWorkspace: Record<string, string[]>;
  setOpen: (open: boolean) => void;
  toggle: () => void;
  setScope: (scope: WorkspaceSearchScope) => void;
  recordRecent: (wsId: string, query: string) => void;
  forgetRecent: (wsId: string, query: string) => void;
}

export const useGlobalSearchStore = create<GlobalSearchState>()(
  persist(
    (set) => ({
      open: false,
      scope: "all",
      recentByWorkspace: {},
      setOpen: (open) => set({ open }),
      toggle: () => set((s) => ({ open: !s.open })),
      setScope: (scope) => set({ scope }),
      recordRecent: (wsId, query) => {
        const q = query.trim();
        if (!q) return;
        set((state) => {
          const bucket = state.recentByWorkspace[wsId] ?? [];
          const next = [q, ...bucket.filter((x) => x.toLowerCase() !== q.toLowerCase())].slice(
            0,
            MAX_RECENT_QUERIES,
          );
          return { recentByWorkspace: { ...state.recentByWorkspace, [wsId]: next } };
        });
      },
      forgetRecent: (wsId, query) =>
        set((state) => {
          const bucket = state.recentByWorkspace[wsId] ?? [];
          return {
            recentByWorkspace: {
              ...state.recentByWorkspace,
              [wsId]: bucket.filter((x) => x !== query),
            },
          };
        }),
    }),
    {
      name: "multica-global-search",
      storage: createJSONStorage(() => defaultStorage),
      partialize: (s) => ({ recentByWorkspace: s.recentByWorkspace }),
    },
  ),
);
