"use client";

import { create } from "zustand";

interface AgentPanelState {
  selectedAgentId: string | null;
  open: (agentId: string) => void;
  close: () => void;
}

/**
 * Global fallback for "open this agent's side panel", used by surfaces that
 * have no local slot to render it inline (Agents list, Squads, Projects,
 * Runtimes, Dashboard, Search, sidebar, member lists, ...). Channels/DM
 * conversations render the panel inline instead (replacing the thread-panel
 * slot, per Frank's direction) via `AgentPanelProvider`/`useOpenAgentPanel` —
 * `ActorAvatar` prefers that local context when present and only falls back
 * to this store when it isn't, so the two mechanisms never both fire for the
 * same click.
 */
export const useAgentPanelStore = create<AgentPanelState>((set) => ({
  selectedAgentId: null,
  open: (agentId) => set({ selectedAgentId: agentId }),
  close: () => set({ selectedAgentId: null }),
}));
