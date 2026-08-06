"use client";

import { create } from "zustand";
import type {
  AgentPanelIdentitySnapshot,
  OpenAgentPanelOptions,
} from "../panel-open";

interface AgentPanelState {
  selectedAgentId: string | null;
  /** Optimistic identity from the click source; cleared on close. */
  identitySnapshot: AgentPanelIdentitySnapshot | null;
  /**
   * LRM-877 — when set, Agent panel shows `← {member}` and pop restores the
   * human Profile dock (global overlay hosts).
   */
  returnToMemberId: string | null;
  open: (
    agentId: string,
    snapshot?: AgentPanelIdentitySnapshot,
    options?: OpenAgentPanelOptions,
  ) => void;
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
 *
 * LRM-292: open is id-driven (optional identity snapshot). Hosts always
 * GET /api/agents/:id for panel body — never gate on ListAgents.find.
 */
export const useAgentPanelStore = create<AgentPanelState>((set) => ({
  selectedAgentId: null,
  identitySnapshot: null,
  returnToMemberId: null,
  open: (agentId, snapshot, options) =>
    set({
      selectedAgentId: agentId,
      identitySnapshot: snapshot ?? null,
      returnToMemberId: options?.returnToMemberId ?? null,
    }),
  close: () =>
    set({
      selectedAgentId: null,
      identitySnapshot: null,
      returnToMemberId: null,
    }),
}));
