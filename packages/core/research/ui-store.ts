import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";
import {
  createWorkspaceAwareStorage,
  registerForWorkspaceRehydration,
} from "../platform/workspace-storage";
import { defaultStorage } from "../platform/storage";

type ResearchUiState = {
  chatDrawerOpen: boolean;
  setChatDrawerOpen: (open: boolean) => void;
};

export const useResearchUiStore = create<ResearchUiState>()(
  persist(
    (set) => ({
      // LRM-1061 / LRM-1056 v2: chat enters closed; FAB reopens a float.
      chatDrawerOpen: false,
      setChatDrawerOpen: (open) => set({ chatDrawerOpen: open }),
    }),
    {
      name: "multica_research_ui_v2",
      storage: createJSONStorage(() => createWorkspaceAwareStorage(defaultStorage)),
      partialize: (s) => ({ chatDrawerOpen: s.chatDrawerOpen }),
    },
  ),
);

registerForWorkspaceRehydration(() => useResearchUiStore.persist.rehydrate());
