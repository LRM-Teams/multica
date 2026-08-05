import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { useWorkspaceId } from "../hooks";
import { dmKeys } from "./queries";
import { invalidateConversations } from "../conversations";
import type { CreateOrFindDMBody, DMItem } from "./types";

/**
 * Create-or-find a 1-on-1 DM with a peer (idempotent). The returned `DMItem`
 * is what callers select/open in the Messages view.
 *
 * On success we SEED the returned DM into the list cache synchronously, THEN
 * invalidate. The seed matters because `useOpenDM` navigates to
 * `/channels/{dm.id}` immediately after this mutation resolves: `channels-page`
 * resolves the active conversation via `dms.find(id)` and, when the id isn't in
 * `dms`, falls back to the system #general channel. Invalidate alone only
 * triggers an ASYNC refetch, so the sync navigation races it — for a
 * freshly-created DM the id isn't in the cached list yet → the picker/hover
 * "Send message" lands the user on #general instead of the new DM (a
 * private→public misroute). Seeding makes the DM resolvable at nav time;
 * invalidate still runs so the server's canonical row/ordering wins once it
 * returns.
 */
export function useCreateOrFindDM() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (body: CreateOrFindDMBody) => api.createOrFindDM(body),
    onSuccess: (dm) => {
      qc.setQueryData<DMItem[]>(dmKeys.list(wsId), (old) =>
        old ? (old.some((d) => d.id === dm.id) ? old : [dm, ...old]) : [dm],
      );
      qc.invalidateQueries({ queryKey: dmKeys.list(wsId) });
      // LRM-1399 — the unified Conversations list is the page's single source;
      // keep it fresh alongside the legacy key that other consumers still read.
      invalidateConversations(qc, wsId);
    },
  });
}

/** Identifies a DM for a row-level operation (pin / unread / close). */
type DMRef = Pick<DMItem, "id" | "source">;

/**
 * Pin / unpin a DM. Persisted as peer-level state on the server, so pinning
 * stays attached to the peer even if the underlying channel is recreated. The
 * list is invalidated on success so the row re-sorts to/from the top.
 */
export function useSetDMPinned() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: ({ source, id, pinned }: DMRef & { pinned: boolean }) =>
      pinned ? api.pinDM(source, id) : api.unpinDM(source, id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: dmKeys.list(wsId) });
      invalidateConversations(qc, wsId);
    },
  });
}

export function useSetDMMuted() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: ({ source, id, muted }: DMRef & { muted: boolean }) =>
      muted ? api.muteDM(source, id) : api.unmuteDM(source, id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: dmKeys.list(wsId) });
      invalidateConversations(qc, wsId);
    },
  });
}

/**
 * Mark a DM as manually unread. The server sets `manual_unread_at` and bumps
 * `unread` to >= 1; opening the conversation later clears it via the existing
 * read path. There is intentionally no "mark read" action - reading is
 * automatic on open.
 */
export function useMarkDMUnread() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: ({ source, id }: DMRef) => api.markDMUnread(source, id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: dmKeys.list(wsId) });
      invalidateConversations(qc, wsId);
    },
  });
}

/** 
 * Close Chat — soft-hide a DM from the user's list. Recoverable: the server
 * keeps history and the conversation reappears when a new message arrives (or
 * the user opens it again from search / a profile). Does not affect the peer.
 */
export function useCloseDM() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: ({ source, id }: DMRef) => api.closeDM(source, id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: dmKeys.list(wsId) });
      invalidateConversations(qc, wsId);
    },
  });
}

/**
 * Mute / unmute a DM. Muted DMs show dimmed unread counts and are excluded
 * from the aggregate badge at the top of the DM section.
 */
export function useMuteDM() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: ({ source, id, muted }: DMRef & { muted: boolean }) =>
      muted ? api.muteDM(source, id) : api.unmuteDM(source, id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: dmKeys.list(wsId) });
      invalidateConversations(qc, wsId);
    },
  });
}
