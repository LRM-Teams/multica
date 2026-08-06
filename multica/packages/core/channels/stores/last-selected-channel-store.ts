"use client";

import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";
import {
  createWorkspaceAwareStorage,
  registerForWorkspaceRehydration,
} from "../../platform/workspace-storage";
import { defaultStorage } from "../../platform/storage";

interface LastSelectedChannelState {
  lastSelectedChannelId: string | null;
  setLastSelectedChannelId: (channelId: string) => void;
  clearLastSelectedChannelId: () => void;
}

/**
 * The last explicitly opened group channel for each workspace.
 *
 * This deliberately excludes DMs: `/channels/[id]` remains the source of
 * truth for every explicit deep link, while the base `/channels` route uses
 * this value only to restore a user's prior group selection after reload.
 */
export const useLastSelectedChannelStore = create<LastSelectedChannelState>()(
  persist(
    (set) => ({
      lastSelectedChannelId: null,
      setLastSelectedChannelId: (channelId) => set({ lastSelectedChannelId: channelId }),
      clearLastSelectedChannelId: () => set({ lastSelectedChannelId: null }),
    }),
    {
      name: "multica_last_selected_channel",
      storage: createJSONStorage(() => createWorkspaceAwareStorage(defaultStorage)),
      partialize: (state) => ({ lastSelectedChannelId: state.lastSelectedChannelId }),
      // A workspace with no saved selection must not inherit the prior
      // workspace's in-memory value while the store rehydrates.
      merge: (persisted, current) => ({
        ...current,
        lastSelectedChannelId:
          (persisted as Partial<LastSelectedChannelState> | undefined)
            ?.lastSelectedChannelId ?? null,
      }),
    },
  ),
);

registerForWorkspaceRehydration(() => useLastSelectedChannelStore.persist.rehydrate());
