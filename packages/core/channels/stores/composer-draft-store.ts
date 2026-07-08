import { create } from "zustand";
import { createJSONStorage, persist, type StateStorage } from "zustand/middleware";
import { createWorkspaceAwareStorage, registerForWorkspaceRehydration } from "../../platform/workspace-storage";
import { defaultStorage } from "../../platform/storage";

/**
 * Per-conversation composer draft persistence — survives conversation
 * switches, tab close, and page reload.
 */
export type ComposerDraftKey = `channel:${string}` | `dm:${string}`;

interface ComposerDraft {
  content: string;
  updatedAt: number;
}

interface ComposerDraftStore {
  drafts: Record<string, ComposerDraft>;
  getDraft: (key: ComposerDraftKey) => string | undefined;
  setDraft: (key: ComposerDraftKey, content: string) => void;
  clearDraft: (key: ComposerDraftKey) => void;
}

// Drafts older than 30 days are dropped on store init. Without TTL the store
// would accumulate every abandoned draft across every channel/DM indefinitely
// and slowly leak localStorage quota.
const TTL_MS = 30 * 24 * 60 * 60 * 1000;

function pruneStaleDrafts(drafts: Record<string, ComposerDraft>): Record<string, ComposerDraft> {
  const cutoff = Date.now() - TTL_MS;
  const out: Record<string, ComposerDraft> = {};
  for (const [k, v] of Object.entries(drafts)) {
    if (v.updatedAt >= cutoff && v.content.trim().length > 0) {
      out[k] = v;
    }
  }
  return out;
}

// The channel composer publishes an update per keystroke (debounceMs=0,
// needed for the typing indicator), so writes to the underlying storage are
// debounced here instead — each call always carries the store's full, current
// serialized state, so debouncing only delays the I/O and never resurrects a
// stale value (e.g. a send-triggered clear right after a keystroke still wins,
// since its `setItem` call replaces the pending one before the delay elapses).
function debounceStorageWrites(storage: StateStorage, delayMs: number): StateStorage {
  let timer: ReturnType<typeof setTimeout> | null = null;
  let pending: { key: string; value: string } | null = null;
  return {
    getItem: (key) => storage.getItem(key),
    setItem: (key, value) => {
      pending = { key, value };
      if (timer) clearTimeout(timer);
      timer = setTimeout(() => {
        if (pending) storage.setItem(pending.key, pending.value);
        timer = null;
        pending = null;
      }, delayMs);
    },
    removeItem: (key) => {
      if (timer) clearTimeout(timer);
      timer = null;
      pending = null;
      storage.removeItem(key);
    },
  };
}

export const useComposerDraftStore = create<ComposerDraftStore>()(
  persist(
    (set, get) => ({
      drafts: {},
      getDraft: (key) => get().drafts[key]?.content,
      setDraft: (key, content) =>
        set((s) => ({
          drafts: { ...s.drafts, [key]: { content, updatedAt: Date.now() } },
        })),
      clearDraft: (key) =>
        set((s) => {
          if (!(key in s.drafts)) return s;
          const next = { ...s.drafts };
          delete next[key];
          return { drafts: next };
        }),
    }),
    {
      name: "multica_channel_composer_drafts",
      storage: createJSONStorage(() =>
        debounceStorageWrites(createWorkspaceAwareStorage(defaultStorage), 300),
      ),
      onRehydrateStorage: () => (state) => {
        if (state) {
          state.drafts = pruneStaleDrafts(state.drafts);
        }
      },
    },
  ),
);

registerForWorkspaceRehydration(() => useComposerDraftStore.persist.rehydrate());
