import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";
import {
  createWorkspaceAwareStorage,
  registerForWorkspaceRehydration,
} from "../platform/workspace-storage";
import { defaultStorage } from "../platform/storage";
import { DEFAULT_RESEARCH_D5_LENS, type ResearchD5Lens } from "./d5-lens";

export type ResearchD5RailMode = "chat" | "detail" | "agent";
export type ResearchD5Overlay = { sessionId: string; kind: "report" };

const MAX_SESSION_PREFERENCES = 20;

function retainRecentSessionPreference(
  current: Record<string, true>,
  sessionId: string,
  enabled: boolean,
): Record<string, true> {
  const previous = Object.entries(current).filter(([key]) => key !== sessionId);
  if (!enabled) return Object.fromEntries(previous);
  return Object.fromEntries([
    ...previous.slice(-(MAX_SESSION_PREFERENCES - 1)),
    [sessionId, true],
  ]);
}

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
  /** Session-scoped transient inspector surface; deliberately not persisted. */
  d5Overlay: ResearchD5Overlay | null;
  setD5Overlay: (overlay: ResearchD5Overlay | null) => void;
  /** Sessions whose compact goal card preference is collapsed. */
  goalCollapsedBySession: Record<string, true>;
  setGoalCollapsed: (sessionId: string, collapsed: boolean) => void;
  /** Sessions whose terminal completion guide was dismissed. */
  completionGuideDismissedBySession: Record<string, true>;
  dismissCompletionGuide: (sessionId: string) => void;
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
      d5Overlay: null,
      setD5Overlay: (overlay) => set({ d5Overlay: overlay }),
      goalCollapsedBySession: {},
      setGoalCollapsed: (sessionId, collapsed) =>
        set((state) => ({
          goalCollapsedBySession: retainRecentSessionPreference(
            state.goalCollapsedBySession,
            sessionId,
            collapsed,
          ),
        })),
      completionGuideDismissedBySession: {},
      dismissCompletionGuide: (sessionId) =>
        set((state) => ({
          completionGuideDismissedBySession: retainRecentSessionPreference(
            state.completionGuideDismissedBySession,
            sessionId,
            true,
          ),
        })),
    }),
    {
      name: "multica_research_ui_v5",
      storage: createJSONStorage(() => createWorkspaceAwareStorage(defaultStorage)),
      partialize: (s) => ({
        d5RailMode: s.d5RailMode === "agent" ? "detail" : s.d5RailMode,
        d5Lens: s.d5Lens,
        goalCollapsedBySession: s.goalCollapsedBySession,
        completionGuideDismissedBySession:
          s.completionGuideDismissedBySession,
      }),
    },
  ),
);

registerForWorkspaceRehydration(() => useResearchUiStore.persist.rehydrate());
