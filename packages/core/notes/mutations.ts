import { useMutation, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { useWorkspaceId } from "../hooks";
import type { CreateNotePageRequest, DuplicateNotePageRequest, MoveNotePageRequest, NotePage, NotePageListResponse, UpdateNotePageRequest, UpdateNotePageSharesRequest } from "../types";
import { collectNoteIdsRemovedOnDelete } from "./delete";
import { noteKeys } from "./queries";

/**
 * Monotonic per-note write epoch. Overlapping autosaves are common while typing;
 * a slower older response must not overwrite a newer optimistic/server write in
 * the React Query cache (that regression syncs into ContentEditor via
 * defaultValue and looks like "typed characters vanished").
 */
const noteUpdateEpoch = new Map<string, number>();

function nextNoteUpdateEpoch(id: string) {
  const epoch = (noteUpdateEpoch.get(id) ?? 0) + 1;
  noteUpdateEpoch.set(id, epoch);
  return epoch;
}

function isCurrentNoteUpdateEpoch(id: string, epoch: number) {
  return noteUpdateEpoch.get(id) === epoch;
}

function applyNotePageToCache(qc: QueryClient, wsId: string, page: NotePage) {
  qc.setQueryData<NotePage>(noteKeys.detail(wsId, page.id), (old) => {
    if (old && old.updated_at > page.updated_at) return old;
    return page;
  });
  qc.setQueryData<NotePageListResponse>(noteKeys.list(wsId), (old) =>
    old
      ? {
          pages: old.pages.map((p) => {
            if (p.id !== page.id) return p;
            if (p.updated_at > page.updated_at) return p;
            return page;
          }),
        }
      : old,
  );
}

export function useCreateNotePage() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (data: CreateNotePageRequest) => api.createNotePage(data),
    onSuccess: (page) => {
      qc.setQueryData<NotePageListResponse>(noteKeys.list(wsId), (old) =>
        old && !old.pages.some((p) => p.id === page.id)
          ? { pages: [...old.pages, page] }
          : old,
      );
      qc.setQueryData(noteKeys.detail(wsId, page.id), page);
    },
    onSettled: () => qc.invalidateQueries({ queryKey: noteKeys.list(wsId) }),
  });
}

export function useDuplicateNotePage() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: ({ id, data }: { id: string; data?: DuplicateNotePageRequest }) => api.duplicateNotePage(id, data),
    onSuccess: (response) => {
      qc.setQueryData<NotePageListResponse>(noteKeys.list(wsId), (old) =>
        old
          ? { pages: [...old.pages.filter((page) => !response.pages.some((copy) => copy.id === page.id)), ...response.pages] }
          : response,
      );
      for (const page of response.pages) qc.setQueryData(noteKeys.detail(wsId, page.id), page);
    },
    onSettled: () => qc.invalidateQueries({ queryKey: noteKeys.list(wsId) }),
  });
}

export function useUpdateNotePage() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: ({ id, data }: { id: string; data: UpdateNotePageRequest }) => api.updateNotePage(id, data),
    onMutate: async ({ id, data }) => {
      const epoch = nextNoteUpdateEpoch(id);
      await qc.cancelQueries({ queryKey: noteKeys.detail(wsId, id) });
      const prevDetail = qc.getQueryData<NotePage>(noteKeys.detail(wsId, id));
      const prevList = qc.getQueryData<NotePageListResponse>(noteKeys.list(wsId));
      const patch = (page: NotePage): NotePage => ({ ...page, ...data });
      qc.setQueryData<NotePage>(noteKeys.detail(wsId, id), (old) => (old ? patch(old) : old));
      qc.setQueryData<NotePageListResponse>(noteKeys.list(wsId), (old) =>
        old ? { pages: old.pages.map((p) => (p.id === id ? patch(p) : p)) } : old,
      );
      return { prevDetail, prevList, id, epoch };
    },
    onError: (_err, _vars, ctx) => {
      // A newer in-flight/completed update owns the cache now — do not roll
      // a superseded failure back over fresher keystrokes.
      if (!ctx || !isCurrentNoteUpdateEpoch(ctx.id, ctx.epoch)) return;
      if (ctx.prevDetail) qc.setQueryData(noteKeys.detail(wsId, ctx.id), ctx.prevDetail);
      if (ctx.prevList) qc.setQueryData(noteKeys.list(wsId), ctx.prevList);
    },
    onSuccess: (page, _vars, ctx) => {
      if (ctx && !isCurrentNoteUpdateEpoch(page.id, ctx.epoch)) return;
      applyNotePageToCache(qc, wsId, page);
    },
    // No onSettled invalidate: autosave is high-frequency, and a refetch that
    // loses the server-side race with an overlapping write would push an older
    // row back into the cache and wipe keystrokes via ContentEditor sync.
    // onMutate/onSuccess already keep list+detail aligned for this client.
  });
}

export function useMoveNotePage() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: ({ id, data }: { id: string; data: MoveNotePageRequest }) => api.moveNotePage(id, data),
    onMutate: async ({ id, data }) => {
      await qc.cancelQueries({ queryKey: noteKeys.list(wsId) });
      const prevDetail = qc.getQueryData<NotePage>(noteKeys.detail(wsId, id));
      const prevList = qc.getQueryData<NotePageListResponse>(noteKeys.list(wsId));
      const patch = (page: NotePage): NotePage => ({ ...page, parent_id: data.parent_id, sort_key: data.sort_key });
      qc.setQueryData<NotePage>(noteKeys.detail(wsId, id), (old) => (old ? patch(old) : old));
      qc.setQueryData<NotePageListResponse>(noteKeys.list(wsId), (old) =>
        old ? { pages: old.pages.map((p) => (p.id === id ? patch(p) : p)) } : old,
      );
      return { prevDetail, prevList, id };
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.prevDetail) qc.setQueryData(noteKeys.detail(wsId, ctx.id), ctx.prevDetail);
      if (ctx?.prevList) qc.setQueryData(noteKeys.list(wsId), ctx.prevList);
    },
    onSuccess: (page) => {
      qc.setQueryData(noteKeys.detail(wsId, page.id), page);
      qc.setQueryData<NotePageListResponse>(noteKeys.list(wsId), (old) =>
        old ? { pages: old.pages.map((p) => (p.id === page.id ? page : p)) } : old,
      );
    },
    onSettled: (_data, _err, vars) => {
      qc.invalidateQueries({ queryKey: noteKeys.detail(wsId, vars.id) });
      qc.invalidateQueries({ queryKey: noteKeys.list(wsId) });
    },
  });
}

export function useUpdateNotePageShares() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: ({ id, data }: { id: string; data: UpdateNotePageSharesRequest }) => api.updateNotePageShares(id, data),
    onSuccess: (page) => {
      qc.setQueryData(noteKeys.detail(wsId, page.id), page);
      qc.setQueryData<NotePageListResponse>(noteKeys.list(wsId), (old) =>
        old ? { pages: old.pages.map((p) => (p.id === page.id ? page : p)) } : old,
      );
    },
    onSettled: (_data, _err, vars) => {
      qc.invalidateQueries({ queryKey: noteKeys.detail(wsId, vars.id) });
      qc.invalidateQueries({ queryKey: noteKeys.list(wsId) });
      qc.invalidateQueries({ queryKey: noteKeys.shareUnreadCount(wsId) });
    },
  });
}

export function useDeleteNotePage() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (id: string) => api.deleteNotePage(id),
    onMutate: async (id) => {
      await qc.cancelQueries({ queryKey: noteKeys.list(wsId) });
      const prevList = qc.getQueryData<NotePageListResponse>(noteKeys.list(wsId));
      const root = prevList?.pages.find((page) => page.id === id);
      const collect = collectNoteIdsRemovedOnDelete(prevList?.pages ?? [], id, root?.can_manage_shares === true);
      qc.setQueryData<NotePageListResponse>(noteKeys.list(wsId), (old) =>
        old ? { pages: old.pages.filter((p) => !collect.has(p.id)) } : old,
      );
      for (const pageId of collect) qc.removeQueries({ queryKey: noteKeys.detail(wsId, pageId) });
      return { prevList };
    },
    onError: (_err, _id, ctx) => {
      if (ctx?.prevList) qc.setQueryData(noteKeys.list(wsId), ctx.prevList);
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: noteKeys.list(wsId) });
      qc.invalidateQueries({ queryKey: noteKeys.trash(wsId) });
    },
  });
}

export function usePermanentlyDeleteNotePage() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (id: string) => api.permanentlyDeleteNotePage(id),
    onMutate: async (id) => {
      await qc.cancelQueries({ queryKey: noteKeys.trash(wsId) });
      const prevTrash = qc.getQueryData<NotePageListResponse>(noteKeys.trash(wsId));
      qc.setQueryData<NotePageListResponse>(noteKeys.trash(wsId), (old) =>
        old ? { pages: old.pages.filter((p) => p.id !== id) } : old,
      );
      return { prevTrash };
    },
    onError: (_err, _id, ctx) => {
      if (ctx?.prevTrash) qc.setQueryData(noteKeys.trash(wsId), ctx.prevTrash);
    },
    onSettled: () => qc.invalidateQueries({ queryKey: noteKeys.trash(wsId) }),
  });
}

export function useEmptyNoteTrash() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: () => api.emptyNoteTrash(),
    onMutate: async () => {
      await qc.cancelQueries({ queryKey: noteKeys.trash(wsId) });
      const prevTrash = qc.getQueryData<NotePageListResponse>(noteKeys.trash(wsId));
      qc.setQueryData<NotePageListResponse>(noteKeys.trash(wsId), { pages: [] });
      return { prevTrash };
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.prevTrash) qc.setQueryData(noteKeys.trash(wsId), ctx.prevTrash);
    },
    onSettled: () => qc.invalidateQueries({ queryKey: noteKeys.trash(wsId) }),
  });
}

export function useRestoreNotePage() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (id: string) => api.restoreNotePage(id),
    onSuccess: (page) => {
      qc.setQueryData<NotePageListResponse>(noteKeys.list(wsId), (old) =>
        old && !old.pages.some((p) => p.id === page.id)
          ? { pages: [...old.pages, page] }
          : old,
      );
      qc.setQueryData<NotePageListResponse>(noteKeys.trash(wsId), (old) =>
        old ? { pages: old.pages.filter((p) => p.id !== page.id) } : old,
      );
      qc.setQueryData(noteKeys.detail(wsId, page.id), page);
    },
    onSettled: (_data, _err, id) => {
      qc.invalidateQueries({ queryKey: noteKeys.list(wsId) });
      qc.invalidateQueries({ queryKey: noteKeys.trash(wsId) });
      qc.invalidateQueries({ queryKey: noteKeys.detail(wsId, id) });
    },
  });
}
