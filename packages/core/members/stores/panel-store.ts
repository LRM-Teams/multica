"use client";

import { create } from "zustand";
import type { MemberPanelIdentitySnapshot } from "../panel-open";

interface MemberPanelState {
  selectedUserId: string | null;
  /** Optimistic identity from the click source; cleared on close. */
  identitySnapshot: MemberPanelIdentitySnapshot | null;
  open: (userId: string, snapshot?: MemberPanelIdentitySnapshot) => void;
  close: () => void;
}

/**
 * Global fallback for "open this human member's profile panel", used by
 * surfaces that have no local slot to render it inline. Channels/DM
 * conversations render the panel inline instead (same exclusive slot as
 * the agent panel) via MemberPanelProvider — ActorAvatar / ActorProfileTrigger
 * prefer that local context when present and only fall back to this store.
 */
export const useMemberPanelStore = create<MemberPanelState>((set) => ({
  selectedUserId: null,
  identitySnapshot: null,
  open: (userId, snapshot) =>
    set({
      selectedUserId: userId,
      identitySnapshot: snapshot ?? null,
    }),
  close: () => set({ selectedUserId: null, identitySnapshot: null }),
}));
