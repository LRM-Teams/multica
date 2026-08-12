import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export const noteKeys = {
  all: (wsId: string) => ["notes", wsId] as const,
  list: (wsId: string) => [...noteKeys.all(wsId), "list"] as const,
  trash: (wsId: string) => [...noteKeys.all(wsId), "trash"] as const,
  detail: (wsId: string, pageId: string) => [...noteKeys.all(wsId), "detail", pageId] as const,
  aiJob: (jobId: string) => ["notes", "ai-job", jobId] as const,
  writebacks: (wsId: string, pageId: string, status?: string) =>
    [...noteKeys.all(wsId), "writebacks", pageId, status ?? "all"] as const,
};

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
