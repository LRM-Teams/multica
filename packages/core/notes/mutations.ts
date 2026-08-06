import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { useWorkspaceId } from "../hooks";
import type { CreateNotePageRequest, DuplicateNotePageRequest, NotePage, NotePageListResponse, UpdateNotePageRequest, UpdateNotePageSharesRequest } from "../types";
import { noteKeys } from "./queries";

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
      await qc.cancelQueries({ queryKey: noteKeys.detail(wsId, id) });
      const prevDetail = qc.getQueryData<NotePage>(noteKeys.detail(wsId, id));
      const prevList = qc.getQueryData<NotePageListResponse>(noteKeys.list(wsId));
      const patch = (page: NotePage): NotePage => ({ ...page, ...data });
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
      const collect = new Set([id]);
      let changed = true;
      while (changed) {
        changed = false;
        for (const page of prevList?.pages ?? []) {
          if (page.parent_id && collect.has(page.parent_id) && !collect.has(page.id)) {
            collect.add(page.id);
            changed = true;
          }
        }
      }
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
