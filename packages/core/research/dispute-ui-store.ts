import { create } from "zustand";

/**
 * LRM-1472 / UI-04 §5 — dispute subgraph client-only display state.
 * Focus/dim/tab are display grouping only and are NEVER written back to the
 * canonical graph. Kept ephemeral (not persisted): focus mode and the active
 * panel tab are per-session presentation, not durable user preference.
 */
export type DisputePanelTab = "overview" | "positions" | "debate" | "verdict";

type DisputeUiState = {
  /** Node under focused detail view (dim ~45% on unrelated nodes). */
  focusNodeId: string | null;
  /** Active tab in the narrow-sheet debate panel. */
  panelTab: DisputePanelTab;
  setFocusNode: (id: string | null) => void;
  setPanelTab: (tab: DisputePanelTab) => void;
  clear: () => void;
};

export const useDisputeUiStore = create<DisputeUiState>((set) => ({
  focusNodeId: null,
  panelTab: "overview",
  setFocusNode: (focusNodeId) => set({ focusNodeId }),
  setPanelTab: (panelTab) => set({ panelTab }),
  clear: () => set({ focusNodeId: null, panelTab: "overview" }),
}));
