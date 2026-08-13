import type { NoteWorkerJobStatus } from "@multica/core/types";

const ACTIVE_NOTE_WORKER_STATUSES = new Set<string>(["pending", "dispatched", "running"]);

/** True while the Worker job may still progress (keep polling). */
export function isNoteWorkerJobActive(status: NoteWorkerJobStatus | string | null | undefined): boolean {
  if (!status) return false;
  return ACTIVE_NOTE_WORKER_STATUSES.has(status);
}

/** Stable i18n leaf for Worker job status labels (notes_page.worker_status_*). */
export function noteWorkerStatusMessageKey(
  status: NoteWorkerJobStatus | string | null | undefined,
): "pending" | "dispatched" | "running" | "completed" | "failed" | "cancelled" | "unknown" {
  switch (status) {
    case "pending":
    case "dispatched":
    case "running":
    case "completed":
    case "failed":
    case "cancelled":
      return status;
    default:
      return "unknown";
  }
}

/** Deep-link target for Worker progress: prefer Messages channel id. */
export function noteWorkerChatTarget(job: {
  agent_id?: string | null;
  channel_id?: string | null;
  chat_session_id?: string | null;
  task_id?: string | null;
}): { agentId: string; channelId: string | null; chatSessionId: string | null; taskId: string | null } {
  return {
    agentId: (job.agent_id ?? "").trim(),
    channelId: job.channel_id?.trim() || null,
    chatSessionId: job.chat_session_id?.trim() || null,
    taskId: job.task_id?.trim() || null,
  };
}

/** @deprecated Prefer opening chat via openNoteWorkerChat; kept for href tests. */
export function noteWorkerRunHref(
  agentId: string,
  taskId: string | null | undefined,
  paths: { agentDetail: (id: string) => string },
  appendQuery: (href: string, params: Record<string, string>) => string,
): string {
  const base = paths.agentDetail(agentId);
  const runId = taskId?.trim();
  if (!runId) return base;
  return appendQuery(base, { run: runId });
}
