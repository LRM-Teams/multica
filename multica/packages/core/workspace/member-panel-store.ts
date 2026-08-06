"use client";

import { create } from "zustand";

interface MemberPanelState {
  selectedUserId: string | null;
  open: (userId: string) => void;
  close: () => void;
}

/**
 * Global fallback for "open this human member's side panel" (LRM-619).
 * Channels/DM prefer a local `MemberPanelProvider`; elsewhere ActorAvatar
 * falls back here — same split as the agent panel store.
 */
export const useMemberPanelStore = create<MemberPanelState>((set) => ({
  selectedUserId: null,
  open: (userId) => set({ selectedUserId: userId }),
  close: () => set({ selectedUserId: null }),
}));
