import { QueryObserver, type QueryClient } from "@tanstack/react-query";
import { noteAIJobOptions } from "@multica/core/notes/queries";
import type { NoteAIEditResult, NoteAIJob, NoteAIJobStatus } from "@multica/core/types";

/**
 * Note AI agents often need >90s for formula-heavy / long-page edits.
 * Local prod jobs after 2026-08-11 routinely finished around 115–133s while the
 * UI had already timed out at 90s — so keep this above that observed range.
 */
export const NOTE_AI_JOB_WAIT_TIMEOUT_MS = 5 * 60 * 1000;

function normalizeNoteAIEditResult(result: NoteAIEditResult): NoteAIEditResult {
  return {
    ...result,
    markdown: result.markdown.trim(),
    target: result.target?.trim() || null,
    title: result.title?.trim() || null,
    rationale: result.rationale?.trim() || null,
  };
}

export function noteAIJobResult(job: NoteAIJob, messages: { failed: string }) {
  if (job.status === "completed") {
    if (job.result?.markdown?.trim()) return normalizeNoteAIEditResult(job.result);
    throw new Error(messages.failed);
  }
  if (job.status === "failed") throw new Error(job.failure_reason || messages.failed);
  if (job.status === "cancelled") throw new DOMException("Note AI job cancelled", "AbortError");
  return null;
}

export async function waitForNoteAIJobResult(
  queryClient: QueryClient,
  jobId: string,
  messages: { failed: string; timeout: string },
  signal?: AbortSignal,
  onStatus?: (status: NoteAIJobStatus) => void,
): Promise<NoteAIEditResult> {
  const initial = queryClient.getQueryData<NoteAIJob>(noteAIJobOptions(jobId).queryKey);
  if (initial) {
    onStatus?.(initial.status);
    const value = noteAIJobResult(initial, messages);
    if (value) return value;
  }

  return new Promise<NoteAIEditResult>((resolve, reject) => {
    const observer = new QueryObserver(queryClient, noteAIJobOptions(jobId));
    let settled = false;
    let unsubscribe: () => void = () => {};
    let timeout: number | null = null;
    const onAbort = () => finish(() => reject(new DOMException("Note AI job cancelled", "AbortError")));
    const cleanup = () => {
      settled = true;
      unsubscribe();
      observer.destroy();
      if (timeout !== null) window.clearTimeout(timeout);
      signal?.removeEventListener("abort", onAbort);
    };
    const finish = (fn: () => void) => {
      if (settled) return;
      cleanup();
      fn();
    };
    timeout = window.setTimeout(() => {
      void queryClient.fetchQuery(noteAIJobOptions(jobId)).then((job) => {
        onStatus?.(job.status);
        try {
          const value = noteAIJobResult(job, messages);
          if (value) finish(() => resolve(value));
          else finish(() => reject(new Error(messages.timeout)));
        } catch (error) {
          finish(() => reject(error));
        }
      }, (error) => finish(() => reject(error)));
    }, NOTE_AI_JOB_WAIT_TIMEOUT_MS);
    unsubscribe = observer.subscribe((result) => {
      if (!result.data) return;
      onStatus?.(result.data.status);
      try {
        const value = noteAIJobResult(result.data, messages);
        if (value) finish(() => resolve(value));
      } catch (error) {
        finish(() => reject(error));
      }
    });
    signal?.addEventListener("abort", onAbort, { once: true });
    if (signal?.aborted) onAbort();
    else void observer.refetch();
  });
}
