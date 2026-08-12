"use client";

import { useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Bot, Check, Loader2 } from "lucide-react";
import { api } from "@multica/core/api";
import { noteWorkerJobOptions } from "@multica/core/notes/queries";
import { resolveActorDisplayName } from "@multica/core/identity";
import { appendQueryParams, useWorkspacePaths } from "@multica/core/paths";
import type { Agent, NoteWorkerJob } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@multica/ui/components/ui/dialog";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { cn } from "@multica/ui/lib/utils";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { toast } from "sonner";
import { useNavigation } from "../navigation";
import { useT } from "../i18n/use-t";
import { noteWorkerRunHref } from "./note-worker-status";

export function NoteWorkerRunDialog({
  pageId,
  agents,
  defaultAgentId,
  open,
  onOpenChange,
  onDispatched,
}: {
  pageId: string;
  agents: Agent[];
  defaultAgentId: string | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onDispatched: (job: NoteWorkerJob) => void;
}) {
  const { t } = useT("layout");
  const queryClient = useQueryClient();
  const paths = useWorkspacePaths();
  const navigation = useNavigation();
  const [agentId, setAgentId] = useState<string | null>(defaultAgentId);
  const [instruction, setInstruction] = useState("");
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    if (!open) return;
    setAgentId(defaultAgentId && agents.some((agent) => agent.id === defaultAgentId) ? defaultAgentId : agents[0]?.id ?? null);
    setInstruction("");
    setSubmitting(false);
  }, [open, defaultAgentId, agents]);

  const submit = async () => {
    const trimmed = instruction.trim();
    if (!agentId) {
      showErrorToast(t(($) => $.notes_page.worker_agent_required));
      return;
    }
    if (!trimmed) {
      showErrorToast(t(($) => $.notes_page.worker_instruction_required));
      return;
    }
    setSubmitting(true);
    try {
      const job = await api.createNoteWorkerJob(pageId, {
        agent_id: agentId,
        instruction: trimmed,
        intent: "worker",
      });
      queryClient.setQueryData(noteWorkerJobOptions(job.id).queryKey, job);
      onDispatched(job);
      onOpenChange(false);
      const href = noteWorkerRunHref(job.agent_id, job.task_id, paths, appendQueryParams);
      toast.success(t(($) => $.notes_page.worker_dispatched), {
        description: t(($) => $.notes_page.worker_dispatched_hint),
        action: {
          label: t(($) => $.notes_page.worker_open_run),
          onClick: () => navigation.push(href),
        },
        duration: 10_000,
      });
    } catch (error: unknown) {
      showErrorToast(error instanceof Error ? error.message : t(($) => $.notes_page.worker_dispatch_failed));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t(($) => $.notes_page.worker_title)}</DialogTitle>
          <DialogDescription>{t(($) => $.notes_page.worker_description)}</DialogDescription>
        </DialogHeader>
        <div className="space-y-4 py-1">
          <div className="space-y-2">
            <div className="text-sm font-medium">{t(($) => $.notes_page.worker_agent_label)}</div>
            <div className="max-h-40 space-y-1 overflow-y-auto rounded-md border p-1">
              {agents.length === 0 ? (
                <div className="rounded-md border border-dashed p-3 text-sm text-muted-foreground">
                  {t(($) => $.notes_page.ai_agent_empty)}
                </div>
              ) : (
                agents.map((agent) => {
                  const selected = agentId === agent.id;
                  const name = resolveActorDisplayName(agent, agent.name || agent.id);
                  return (
                    <button
                      key={agent.id}
                      type="button"
                      className={cn(
                        "flex w-full items-center gap-3 rounded-lg px-3 py-2 text-left text-sm hover:bg-muted/70",
                        selected && "bg-muted text-foreground",
                      )}
                      onClick={() => setAgentId(agent.id)}
                      disabled={submitting}
                    >
                      <Bot className="size-4 shrink-0 text-muted-foreground" />
                      <span className="min-w-0 flex-1 truncate">{name}</span>
                      {selected && <Check className="size-4 text-primary" />}
                    </button>
                  );
                })
              )}
            </div>
          </div>
          <div className="space-y-2">
            <div className="text-sm font-medium">{t(($) => $.notes_page.worker_instruction_label)}</div>
            <Textarea
              value={instruction}
              onChange={(event) => setInstruction(event.target.value)}
              placeholder={t(($) => $.notes_page.worker_instruction_placeholder)}
              rows={4}
              disabled={submitting}
            />
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={submitting}>
            {t(($) => $.notes_page.cancel)}
          </Button>
          <Button onClick={() => void submit()} disabled={submitting || agents.length === 0}>
            {submitting ? <Loader2 className="size-4 animate-spin" /> : null}
            {t(($) => $.notes_page.worker_submit)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
