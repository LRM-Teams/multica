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
