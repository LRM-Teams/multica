"use client";

import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";
import { defaultStorage } from "@multica/core/platform";
import type { WorkspaceSearchScope } from "@multica/core/types";

const MAX_RECENT_QUERIES = 8;

/** from:@ chip — member or agent author (LRM-873). */
export type SearchFromAuthor = {
  author_type: "user" | "agent";
  author_id: string;
  /** Display label for the chip, e.g. @andong3 or agent display name. */
  label: string;
};

interface GlobalSearchState {
  open: boolean;
  scope: WorkspaceSearchScope;
  /** Recent search queries, namespaced by workspace id (see RecentIssuesStore). */
  recentByWorkspace: Record<string, string[]>;
  /** LRM-873 from:@ author filter. */
  fromAuthor: SearchFromAuthor | null;
  /** Default true — include thread replies in message hits. */
  includeThread: boolean;
  /** When set, "本频道" search can call channel search API. */
  channelId: string | null;
  /** channel = current channel only; workspace = whole workspace. */
  messageRange: "channel" | "workspace";
  setOpen: (open: boolean) => void;
  toggle: () => void;
  setScope: (scope: WorkspaceSearchScope) => void;
  setFromAuthor: (author: SearchFromAuthor | null) => void;
  setIncludeThread: (include: boolean) => void;
  setMessageRange: (range: "channel" | "workspace") => void;
  setChannelId: (channelId: string | null) => void;
  /**
   * Open search pre-filled for from:@ / `/from` (LRM-873).
   * Forces messages scope; optional channelId enables 本频道 default.
   */
  openFromAuthor: (args: {
    author: SearchFromAuthor;
    channelId?: string | null;
    messageRange?: "channel" | "workspace";
  }) => void;
  recordRecent: (wsId: string, query: string) => void;
  forgetRecent: (wsId: string, query: string) => void;
}

export const useGlobalSearchStore = create<GlobalSearchState>()(
  persist(
    (set) => ({
      open: false,
      scope: "all",
      recentByWorkspace: {},
      fromAuthor: null,
      includeThread: true,
      channelId: null,
      messageRange: "workspace",
      setOpen: (open) =>
        set((s) =>
          open
            ? { open: true }
            : {
                open: false,
                fromAuthor: null,
                channelId: null,
                messageRange: "workspace",
                includeThread: true,
                scope: s.scope,
              },
        ),
      toggle: () => set((s) => ({ open: !s.open })),
      setScope: (scope) => set({ scope }),
      setFromAuthor: (fromAuthor) =>
        set({
          fromAuthor,
          ...(fromAuthor ? { scope: "messages" as const } : {}),
        }),
      setIncludeThread: (includeThread) => set({ includeThread }),
      setMessageRange: (messageRange) => set({ messageRange }),
      setChannelId: (channelId) =>
        set((s) => ({
          channelId,
          messageRange: channelId
            ? s.channelId
              ? s.messageRange
              : "channel"
            : "workspace",
        })),
      openFromAuthor: ({ author, channelId, messageRange }) =>
        set({
          open: true,
          fromAuthor: author,
          scope: "messages",
          channelId: channelId ?? null,
          messageRange:
            messageRange ??
            (channelId ? "channel" : "workspace"),
          includeThread: true,
        }),
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
