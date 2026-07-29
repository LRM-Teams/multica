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
      chatDrawerOpen: true,
      setChatDrawerOpen: (open) => set({ chatDrawerOpen: open }),
    }),
    {
      name: "multica_research_ui",
      storage: createJSONStorage(() => createWorkspaceAwareStorage(defaultStorage)),
      partialize: (s) => ({ chatDrawerOpen: s.chatDrawerOpen }),
    },
  ),
);

registerForWorkspaceRehydration(() => useResearchUiStore.persist.rehydrate());
