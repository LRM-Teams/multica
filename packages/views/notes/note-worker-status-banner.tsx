"use client";

import { useQuery } from "@tanstack/react-query";
import { Bot, Loader2 } from "lucide-react";
import { noteWorkerJobOptions } from "@multica/core/notes/queries";
import { useWorkspacePaths } from "@multica/core/paths";
import { Button } from "@multica/ui/components/ui/button";
import { AppLink } from "../navigation";
import { useT } from "../i18n/use-t";
import { isNoteWorkerJobActive, noteWorkerStatusMessageKey } from "./note-worker-status";

export function NoteWorkerStatusBanner({ jobId }: { jobId: string }) {
  const { t } = useT("layout");
  const paths = useWorkspacePaths();
  const { data: job } = useQuery(noteWorkerJobOptions(jobId));
  if (!job?.id) return null;

  const statusKey = noteWorkerStatusMessageKey(job.status);
  const statusLabel =
    statusKey === "pending"
      ? t(($) => $.notes_page.worker_status_pending)
      : statusKey === "dispatched"
        ? t(($) => $.notes_page.worker_status_dispatched)
        : statusKey === "running"
          ? t(($) => $.notes_page.worker_status_running)
          : statusKey === "completed"
            ? t(($) => $.notes_page.worker_status_completed)
            : statusKey === "failed"
              ? t(($) => $.notes_page.worker_status_failed)
              : statusKey === "cancelled"
                ? t(($) => $.notes_page.worker_status_cancelled)
                : t(($) => $.notes_page.worker_status_unknown);

  const active = isNoteWorkerJobActive(job.status);

  return (
    <div className="mb-4 flex flex-wrap items-center gap-3 rounded-lg border bg-muted/30 px-3 py-2 text-sm">
      <div className="flex min-w-0 flex-1 items-center gap-2">
        {active ? <Loader2 className="size-4 shrink-0 animate-spin text-muted-foreground" /> : <Bot className="size-4 shrink-0 text-muted-foreground" />}
        <span className="min-w-0 truncate">
          {t(($) => $.notes_page.worker_status_prefix)}
          <span className="font-medium text-foreground">{statusLabel}</span>
          {job.failure_reason ? <span className="text-muted-foreground"> — {job.failure_reason}</span> : null}
        </span>
      </div>
      <Button variant="outline" size="sm" render={<AppLink href={paths.agentDetail(job.agent_id)} />}>
        {t(($) => $.notes_page.worker_open_agent)}
      </Button>
    </div>
  );
}
