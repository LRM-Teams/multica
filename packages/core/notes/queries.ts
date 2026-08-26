import { queryOptions, useQuery, type QueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { NotePage, NotePageListResponse } from "../types";

export const noteKeys = {
  all: (wsId: string) => ["notes", wsId] as const,
  list: (wsId: string) => [...noteKeys.all(wsId), "list"] as const,
  trash: (wsId: string) => [...noteKeys.all(wsId), "trash"] as const,
  detail: (wsId: string, pageId: string) => [...noteKeys.all(wsId), "detail", pageId] as const,
  shareUnreadCount: (wsId: string) => [...noteKeys.all(wsId), "share-unread-count"] as const,
  aiJob: (jobId: string) => ["notes", "ai-job", jobId] as const,
  writebacks: (wsId: string, pageId: string, status?: string) =>
    [...noteKeys.all(wsId), "writebacks", pageId, status ?? "all"] as const,
  periodBriefActive: (wsId: string, pageId: string) =>
    [...noteKeys.all(wsId), "period-brief-active", pageId] as const,
};

export function noteShareUnreadCountOptions(wsId: string) {
  return queryOptions({
    queryKey: noteKeys.shareUnreadCount(wsId),
    queryFn: () => api.countNoteShareUnread(),
    enabled: !!wsId,
  });
}

/** Sidebar badge: unread direct note shares the current user has not opened. */
export function useNoteShareUnreadCount(wsId: string | null | undefined): number {
  const { data } = useQuery({
    ...noteShareUnreadCountOptions(wsId ?? ""),
    enabled: !!wsId,
    select: (response) => response.count,
  });
  return data ?? 0;
}

/** Only unread shared pages need the seen write + list patch. */
export function noteNeedsShareSeen(page?: NotePage | null): boolean {
  return Boolean(page?.share_unread);
}

/**
 * GET /pages/:id marks the share seen. Mirror that onto the list and unread
 * count so the tree dot and sidebar badge clear without waiting on refetch.
 */
export function applyNoteShareSeen(qc: QueryClient, wsId: string, pageId: string): void {
  const listedPage = qc.getQueryData<NotePageListResponse>(noteKeys.list(wsId))?.pages.find((page) => page.id === pageId);
  if (listedPage && !listedPage.share_unread) {
    return;
  }
  let wasUnread = false;
  qc.setQueryData<NotePageListResponse>(noteKeys.list(wsId), (old) => {
    if (!old) return old;
    let changed = false;
    const pages = old.pages.map((page) => {
      if (page.id !== pageId || !page.share_unread) return page;
      changed = true;
      return { ...page, share_unread: false };
    });
    wasUnread = wasUnread || changed;
    return changed ? { ...old, pages } : old;
  });
  qc.setQueryData<NotePage>(noteKeys.detail(wsId, pageId), (old) => {
    if (!old?.share_unread) return old;
    wasUnread = true;
    return { ...old, share_unread: false };
  });
  const listed = qc.getQueryData<NotePageListResponse>(noteKeys.list(wsId))?.pages.some((page) => page.id === pageId) ?? false;
  if (wasUnread) {
    qc.setQueryData<{ count: number }>(noteKeys.shareUnreadCount(wsId), (old) =>
      old ? { count: Math.max(0, old.count - 1) } : old,
    );
  }
  if (wasUnread || !listed) {
    qc.invalidateQueries({ queryKey: noteKeys.shareUnreadCount(wsId) });
  }
}

export function noteListOptions(wsId: string) {
  return queryOptions({
    queryKey: noteKeys.list(wsId),
    queryFn: () => api.listNotePages(),
    enabled: !!wsId,
  });
}

export function noteTrashOptions(wsId: string) {
  return queryOptions({
    queryKey: noteKeys.trash(wsId),
    queryFn: () => api.listDeletedNotePages(),
    enabled: !!wsId,
  });
}

export function noteDetailOptions(wsId: string, pageId: string) {
  return queryOptions({
    queryKey: noteKeys.detail(wsId, pageId),
    queryFn: () => api.getNotePage(pageId),
    enabled: !!wsId && !!pageId,
    retry: (count, err) => {
      const status = typeof err === "object" && err && "status" in err ? Number((err as { status: number }).status) : 0;
      if (status === 403 || status === 404) return false;
      return count < 2;
    },
  });
}

export function noteAIJobOptions(jobId: string) {
  return queryOptions({
    queryKey: noteKeys.aiJob(jobId),
    queryFn: () => api.getNoteAIJob(jobId),
    enabled: !!jobId,
    staleTime: Infinity,
    retry: (count, err) => {
      const status = typeof err === "object" && err && "status" in err ? Number((err as { status: number }).status) : 0;
      if (status === 403 || status === 404) return false;
      return count < 2;
    },
  });
}

export function notePeriodBriefActiveOptions(wsId: string, pageId: string) {
  return queryOptions({
    queryKey: noteKeys.periodBriefActive(wsId, pageId),
    queryFn: () => api.getActiveNotePeriodBrief(pageId),
    enabled: !!wsId && !!pageId,
    refetchInterval: (query) => {
      const status = query.state.data?.run?.status;
      return status === "planning" || status === "collecting" || status === "synthesizing"
        ? 4_000
        : false;
    },
  });
}

export function noteWritebacksOptions(wsId: string, pageId: string, status: "pending" | "applied" | "rejected" | "all" = "pending") {
  const filter = status === "all" ? undefined : status;
  return queryOptions({
    queryKey: noteKeys.writebacks(wsId, pageId, filter),
    queryFn: () => api.listNotePageWritebacks(pageId, filter),
    enabled: !!wsId && !!pageId,
    refetchInterval: (query) => {
      const count = query.state.data?.writebacks?.length ?? 0;
      return count > 0 ? 15_000 : false;
    },
  });
}
