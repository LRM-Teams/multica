"use client";

import { useEffect, useMemo, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Bot, Check, Hash, Loader2 } from "lucide-react";
import { api } from "@multica/core/api";
import { channelMembersOptions, channelsOptions } from "@multica/core/channels/queries";
import { noteWorkerJobOptions } from "@multica/core/notes/queries";
import { resolveActorDisplayName } from "@multica/core/identity";
import { useWorkspaceId } from "@multica/core/hooks";
import type { Agent, NoteWorkerJob } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@multica/ui/components/ui/dialog";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { cn } from "@multica/ui/lib/utils";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { useT } from "../i18n/use-t";
import { useOpenNoteWorkerChat } from "./use-open-note-worker-chat";

type DestinationKind = "agent" | "channel";

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
  const wsId = useWorkspaceId();
  const { openNoteWorkerChat } = useOpenNoteWorkerChat();
  const [destinationKind, setDestinationKind] = useState<DestinationKind>("agent");
  const [agentId, setAgentId] = useState<string | null>(defaultAgentId);
  const [channelId, setChannelId] = useState<string | null>(null);
  const [instruction, setInstruction] = useState("");
  const [submitting, setSubmitting] = useState(false);

  const { data: channels = [] } = useQuery({
    ...channelsOptions(wsId ?? ""),
    enabled: open && !!wsId,
  });
  const { data: channelMembers = [] } = useQuery({
    ...channelMembersOptions(channelId ?? ""),
    enabled: open && destinationKind === "channel" && !!channelId,
  });

  const channelAgents = useMemo(() => {
    const ids = new Set(
      channelMembers.filter((member) => member.member_type === "agent").map((member) => member.member_id),
    );
    return agents.filter((agent) => ids.has(agent.id));
  }, [agents, channelMembers]);

  useEffect(() => {
    if (!open) return;
    setDestinationKind("agent");
    setAgentId(defaultAgentId && agents.some((agent) => agent.id === defaultAgentId) ? defaultAgentId : agents[0]?.id ?? null);
    setChannelId(null);
    setInstruction("");
    setSubmitting(false);
  }, [open, defaultAgentId, agents]);

  useEffect(() => {
    if (destinationKind !== "channel") return;
    if (channelAgents.some((agent) => agent.id === agentId)) return;
    setAgentId(channelAgents[0]?.id ?? null);
  }, [destinationKind, channelAgents, agentId]);

  const submit = async () => {
    const trimmed = instruction.trim();
    if (destinationKind === "channel" && !channelId) {
      showErrorToast(t(($) => $.notes_page.worker_channel_required));
      return;
    }
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
        ...(destinationKind === "channel" && channelId ? { channel_id: channelId } : {}),
      });
      queryClient.setQueryData(noteWorkerJobOptions(job.id).queryKey, job);
      onDispatched(job);
      onOpenChange(false);
      void openNoteWorkerChat(job);
    } catch (error: unknown) {
      showErrorToast(error instanceof Error ? error.message : t(($) => $.notes_page.worker_dispatch_failed));
    } finally {
      setSubmitting(false);
    }
  };

  const agentChoices = destinationKind === "channel" ? channelAgents : agents;
  const canSubmit =
    !submitting &&
    !!agentId &&
    (destinationKind === "agent" || !!channelId) &&
    agentChoices.length > 0;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t(($) => $.notes_page.worker_title)}</DialogTitle>
          <DialogDescription>{t(($) => $.notes_page.worker_description)}</DialogDescription>
        </DialogHeader>
        <div className="space-y-4 py-1">
          <div className="space-y-2">
            <div className="text-sm font-medium">{t(($) => $.notes_page.worker_destination_label)}</div>
            <div className="flex gap-1 rounded-md border p-1">
              <button
                type="button"
                className={cn(
                  "flex-1 rounded-md px-3 py-1.5 text-sm",
                  destinationKind === "agent" ? "bg-muted font-medium" : "text-muted-foreground hover:bg-muted/50",
                )}
                onClick={() => setDestinationKind("agent")}
                disabled={submitting}
              >
                {t(($) => $.notes_page.worker_destination_agent)}
              </button>
              <button
                type="button"
                className={cn(
                  "flex-1 rounded-md px-3 py-1.5 text-sm",
                  destinationKind === "channel" ? "bg-muted font-medium" : "text-muted-foreground hover:bg-muted/50",
                )}
                onClick={() => setDestinationKind("channel")}
                disabled={submitting}
              >
                {t(($) => $.notes_page.worker_destination_channel)}
              </button>
            </div>
          </div>

          {destinationKind === "channel" ? (
            <div className="space-y-2">
              <div className="text-sm font-medium">{t(($) => $.notes_page.worker_channel_label)}</div>
              <div className="max-h-36 space-y-1 overflow-y-auto rounded-md border p-1">
                {channels.length === 0 ? (
                  <div className="rounded-md border border-dashed p-3 text-sm text-muted-foreground">
                    {t(($) => $.notes_page.worker_channel_empty)}
                  </div>
                ) : (
                  channels.map((channel) => {
                    const selected = channelId === channel.id;
                    return (
                      <button
                        key={channel.id}
                        type="button"
                        className={cn(
                          "flex w-full items-center gap-3 rounded-lg px-3 py-2 text-left text-sm hover:bg-muted/70",
                          selected && "bg-muted text-foreground",
                        )}
                        onClick={() => setChannelId(channel.id)}
                        disabled={submitting}
                      >
                        <Hash className="size-4 shrink-0 text-muted-foreground" />
                        <span className="min-w-0 flex-1 truncate">{channel.name}</span>
                        {selected && <Check className="size-4 text-primary" />}
                      </button>
                    );
                  })
                )}
              </div>
            </div>
          ) : null}

          <div className="space-y-2">
            <div className="text-sm font-medium">
              {destinationKind === "channel"
                ? t(($) => $.notes_page.worker_channel_agent_label)
                : t(($) => $.notes_page.worker_agent_label)}
            </div>
            <div className="max-h-40 space-y-1 overflow-y-auto rounded-md border p-1">
              {agentChoices.length === 0 ? (
                <div className="rounded-md border border-dashed p-3 text-sm text-muted-foreground">
                  {destinationKind === "channel"
                    ? t(($) => $.notes_page.worker_channel_agent_empty)
                    : t(($) => $.notes_page.ai_agent_empty)}
                </div>
              ) : (
                agentChoices.map((agent) => {
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
              onKeyDown={(event) => {
                if (
                  event.key !== "Enter" ||
                  event.shiftKey ||
                  event.nativeEvent.isComposing ||
                  !canSubmit
                ) {
                  return;
                }
                event.preventDefault();
                void submit();
              }}
              placeholder={t(($) => $.notes_page.worker_instruction_placeholder)}
              rows={4}
              disabled={submitting}
            />
            <p className="text-xs text-muted-foreground">{t(($) => $.notes_page.worker_instruction_hint)}</p>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={submitting}>
            {t(($) => $.notes_page.cancel)}
          </Button>
          <Button onClick={() => void submit()} disabled={!canSubmit}>
            {submitting ? <Loader2 className="size-4 animate-spin" /> : null}
            {t(($) => $.notes_page.worker_submit)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
