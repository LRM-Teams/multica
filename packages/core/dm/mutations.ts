import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { useWorkspaceId } from "../hooks";
import { dmKeys } from "./queries";
import type { CreateOrFindDMBody, DMItem } from "./types";

/**
 * Create-or-find a 1-on-1 DM with a peer (idempotent). On success the DM list
 * is invalidated so a freshly created DM appears in the sidebar. The returned
 * `DMItem` is what callers select/open in the Messages view.
 */
export function useCreateOrFindDM() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (body: CreateOrFindDMBody) => api.createOrFindDM(body),
    onSuccess: () => qc.invalidateQueries({ queryKey: dmKeys.list(wsId) }),
  });
}

/** Identifies a DM for a row-level operation (pin / unread / close). */
type DMRef = Pick<DMItem, "id" | "source">;

/**
 * Pin / unpin a DM. Persisted as peer-level state on the server, so pinning
 * dedupes across the peer's dm_channel + legacy_session sources. The list is
 * invalidated on success so the row re-sorts to/from the top.
 */
export function useSetDMPinned() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: ({ source, id, pinned }: DMRef & { pinned: boolean }) =>
      pinned ? api.pinDM(source, id) : api.unpinDM(source, id),
    onSuccess: () => qc.invalidateQueries({ queryKey: dmKeys.list(wsId) }),
  });
}

export function useSetDMMuted() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: ({ source, id, muted }: DMRef & { muted: boolean }) =>
      muted ? api.muteDM(source, id) : api.unmuteDM(source, id),
    onSuccess: () => qc.invalidateQueries({ queryKey: dmKeys.list(wsId) }),
  });
}

/**
 * Mark a DM as manually unread. The server sets `manual_unread_at` and bumps
 * `unread` to >= 1; opening the conversation later clears it via the existing
 * read path. There is intentionally no "mark read" action — reading is
 * automatic on open.
 */
export function useMarkDMUnread() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: ({ source, id }: DMRef) => api.markDMUnread(source, id),
    onSuccess: () => qc.invalidateQueries({ queryKey: dmKeys.list(wsId) }),
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
    onSuccess: () => qc.invalidateQueries({ queryKey: dmKeys.list(wsId) }),
  });
}
