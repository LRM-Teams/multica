import { create } from "zustand";
import { createJSONStorage, persist, type StateStorage } from "zustand/middleware";
import { createWorkspaceAwareStorage, registerForWorkspaceRehydration } from "../../platform/workspace-storage";
import { defaultStorage } from "../../platform/storage";

/**
 * Per-conversation composer draft persistence — survives conversation
 * switches, tab close, and page reload.
 */
export type ComposerDraftKey = `channel:${string}` | `dm:${string}`;

/**
 * LRM-801 — tray attachments ride the same draft lifecycle as text.
 * Only *finished* uploads are restorable (public link survives reload, and
 * the attachment_id can go straight onto the wire); anything still
 * uploading/failed at leave time comes back as an `unrestorable` placeholder
 * ("需重新选择") that never blocks the text draft.
 */
export interface ComposerDraftAttachment {
  attachmentId?: string;
  filename: string;
  contentType: string;
  sizeBytes: number;
  /** Remote (non-blob) preview link — blob URLs die with the tab. */
  previewUrl?: string;
  unrestorable?: boolean;
}

interface ComposerDraft {
  content: string;
  attachments?: ComposerDraftAttachment[];
  updatedAt: number;
}

interface ComposerDraftStore {
  drafts: Record<string, ComposerDraft>;
  getDraft: (key: ComposerDraftKey) => string | undefined;
  setDraft: (key: ComposerDraftKey, content: string) => void;
  /** Text-only clear (user deleted the text): attachments survive. */
  clearDraftContent: (key: ComposerDraftKey) => void;
  setDraftAttachments: (key: ComposerDraftKey, attachments: ComposerDraftAttachment[]) => void;
  /** Full clear (send success / manual clear): text + attachments together. */
  clearDraft: (key: ComposerDraftKey) => void;
}

// Drafts older than 30 days are dropped on store init. Without TTL the store
// would accumulate every abandoned draft across every channel/DM indefinitely
// and slowly leak localStorage quota.
const TTL_MS = 30 * 24 * 60 * 60 * 1000;

function isDraftEmpty(draft: ComposerDraft): boolean {
  return draft.content.trim().length === 0 && (draft.attachments?.length ?? 0) === 0;
}

function pruneStaleDrafts(drafts: Record<string, ComposerDraft>): Record<string, ComposerDraft> {
  const cutoff = Date.now() - TTL_MS;
  const out: Record<string, ComposerDraft> = {};
  for (const [k, v] of Object.entries(drafts)) {
    if (v.updatedAt >= cutoff && !isDraftEmpty(v)) {
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
        set((s) => {
          const existing = s.drafts[key];
          const next: ComposerDraft = {
            content,
            attachments: existing?.attachments,
            updatedAt: Date.now(),
          };
          const drafts = { ...s.drafts };
          if (isDraftEmpty(next)) delete drafts[key];
          else drafts[key] = next;
          return { drafts };
        }),
      clearDraftContent: (key) =>
        set((s) => {
          const existing = s.drafts[key];
          if (!existing) return s;
          const drafts = { ...s.drafts };
          if ((existing.attachments?.length ?? 0) === 0) delete drafts[key];
          else drafts[key] = { ...existing, content: "", updatedAt: Date.now() };
          return { drafts };
        }),
      setDraftAttachments: (key, attachments) =>
        set((s) => {
          const existing = s.drafts[key];
          const next: ComposerDraft = {
            content: existing?.content ?? "",
            attachments: attachments.length > 0 ? attachments : undefined,
            updatedAt: Date.now(),
          };
          const drafts = { ...s.drafts };
          if (isDraftEmpty(next)) delete drafts[key];
          else drafts[key] = next;
          return { drafts };
        }),
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
