import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";
import {
  createWorkspaceAwareStorage,
  registerForWorkspaceRehydration,
} from "../platform/workspace-storage";
import { defaultStorage } from "../platform/storage";
import { DEFAULT_RESEARCH_D5_LENS, type ResearchD5Lens } from "./d5-lens";

export type ResearchD5RailMode = "chat" | "detail";

type ResearchUiState = {
  chatDrawerOpen: boolean;
  setChatDrawerOpen: (open: boolean) => void;
  /** D5 desktop/mobile rail visibility (client chrome, not canonical graph). */
  d5RailOpen: boolean;
  setD5RailOpen: (open: boolean) => void;
  /** Which panel occupies the shared D5 rail. */
  d5RailMode: ResearchD5RailMode;
  setD5RailMode: (mode: ResearchD5RailMode) => void;
  /** Active D5 display lens (URL + store; display-only). */
  d5Lens: ResearchD5Lens;
  setD5Lens: (lens: ResearchD5Lens) => void;
};

export const useResearchUiStore = create<ResearchUiState>()(
  persist(
    (set) => ({
      // LRM-1061 / LRM-1056 v2: chat enters closed; FAB reopens a float.
      chatDrawerOpen: false,
      setChatDrawerOpen: (open) => set({ chatDrawerOpen: open }),
      d5RailOpen: true,
      setD5RailOpen: (open) => set({ d5RailOpen: open }),
      d5RailMode: "chat",
      setD5RailMode: (mode) => set({ d5RailMode: mode }),
      d5Lens: DEFAULT_RESEARCH_D5_LENS,
      setD5Lens: (lens) => set({ d5Lens: lens }),
    }),
    {
      name: "multica_research_ui_v4",
      storage: createJSONStorage(() => createWorkspaceAwareStorage(defaultStorage)),
      partialize: (s) => ({
        chatDrawerOpen: s.chatDrawerOpen,
        d5RailOpen: s.d5RailOpen,
        d5RailMode: s.d5RailMode,
        d5Lens: s.d5Lens,
      }),
    },
  ),
);

registerForWorkspaceRehydration(() => useResearchUiStore.persist.rehydrate());
