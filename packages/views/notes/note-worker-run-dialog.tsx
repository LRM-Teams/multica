"use client";

import { useMemo, useReducer, useRef } from "react";
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
import {
  NOTE_WORKER_PLAYBOOKS,
  type NoteWorkerPlaybookId,
  noteWorkerPlaybookById,
} from "./note-worker-playbooks";
import { useOpenNoteWorkerChat } from "./use-open-note-worker-chat";

type DestinationKind = "agent" | "channel";

type DialogState = {
  destinationKind: DestinationKind;
  agentId: string | null;
  channelId: string | null;
  instruction: string;
  playbookId: NoteWorkerPlaybookId | null;
  submitting: boolean;
};

type DialogAction =
  | { type: "reset"; agentId: string | null }
  | { type: "setDestination"; kind: DestinationKind; agentId?: string | null }
  | { type: "setAgentId"; agentId: string | null }
  | { type: "setChannelId"; channelId: string | null }
  | { type: "setInstruction"; instruction: string }
  | {
      type: "applyPlaybook";
      playbookId: NoteWorkerPlaybookId;
      instruction: string;
      prefersChannel: boolean;
      agentId: string | null;
    }
  | { type: "setSubmitting"; submitting: boolean };

function dialogReducer(state: DialogState, action: DialogAction): DialogState {
  switch (action.type) {
    case "reset":
      return {
        destinationKind: "agent",
        agentId: action.agentId,
        channelId: null,
        instruction: "",
        playbookId: null,
        submitting: false,
      };
    case "setDestination":
      return {
        ...state,
        destinationKind: action.kind,
        playbookId:
          action.kind === "agent" && state.playbookId
            ? noteWorkerPlaybookById(state.playbookId)?.prefersChannel
              ? null
              : state.playbookId
            : state.playbookId,
        ...(action.agentId !== undefined ? { agentId: action.agentId } : {}),
      };
    case "setAgentId":
      return { ...state, agentId: action.agentId };
    case "setChannelId":
      return { ...state, channelId: action.channelId, agentId: null };
    case "setInstruction":
      return { ...state, instruction: action.instruction, playbookId: null };
    case "applyPlaybook":
      return {
        ...state,
        playbookId: action.playbookId,
        instruction: action.instruction,
        destinationKind: action.prefersChannel ? "channel" : state.destinationKind,
        ...(action.prefersChannel && action.agentId
          ? { agentId: action.agentId }
          : {}),
      };
    case "setSubmitting":
      return { ...state, submitting: action.submitting };
    default:
      return state;
  }
}

function playbookLabelKey(id: NoteWorkerPlaybookId) {
  switch (id) {
    case "coordinate":
      return "worker_playbook_coordinate" as const;
    case "hire":
      return "worker_playbook_hire" as const;
    case "writeback":
      return "worker_playbook_writeback" as const;
    case "period_brief":
      return "worker_playbook_period_brief" as const;
  }
}

function playbookInstructionKey(id: NoteWorkerPlaybookId) {
  switch (id) {
    case "coordinate":
      return "worker_playbook_coordinate_instruction" as const;
    case "hire":
      return "worker_playbook_hire_instruction" as const;
    case "writeback":
      return "worker_playbook_writeback_instruction" as const;
    case "period_brief":
      return "worker_playbook_period_brief_instruction" as const;
  }
}

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
  const [state, dispatch] = useReducer(dialogReducer, {
    destinationKind: "agent" as DestinationKind,
    agentId: defaultAgentId,
    channelId: null,
    instruction: "",
    playbookId: null,
    submitting: false,
  });
  const { destinationKind, agentId, channelId, instruction, playbookId, submitting } = state;

  const preferredAgentId =
    defaultAgentId && agents.some((agent) => agent.id === defaultAgentId)
      ? defaultAgentId
      : agents[0]?.id ?? null;

  // Reset when the dialog opens — adjust during render (prev ref), not an effect,
  // so we never paint a stale destination/instruction after reopen.
  const prevOpenRef = useRef(open);
  if (open !== prevOpenRef.current) {
    prevOpenRef.current = open;
    if (open) dispatch({ type: "reset", agentId: preferredAgentId });
  } else if (open && destinationKind === "agent" && preferredAgentId && !agentId) {
    // Agents may arrive after open; fill once without wiping a user pick.
    dispatch({ type: "setAgentId", agentId: preferredAgentId });
  }

  const { data: channels = [] } = useQuery({
    ...channelsOptions(wsId ?? ""),
    enabled: open && !!wsId,
  });
  const { data: channelMembers = [] } = useQuery({
    ...channelMembersOptions(channelId ?? ""),
    enabled: open && destinationKind === "channel" && !!channelId,
  });

  const channelAgents = useMemo(() => {
    const ids = new Set<string>();
    for (const member of channelMembers) {
      if (member.member_type === "agent") ids.add(member.member_id);
    }
    return agents.filter((agent) => ids.has(agent.id));
  }, [agents, channelMembers]);

  const channelAgentId =
    destinationKind === "channel"
      ? channelAgents.some((agent) => agent.id === agentId)
        ? agentId
        : channelAgents[0]?.id ?? null
      : agentId;
  if (open && destinationKind === "channel" && channelAgentId !== agentId) {
    dispatch({ type: "setAgentId", agentId: channelAgentId });
  }

  const activePlaybook = noteWorkerPlaybookById(playbookId);
  const showChannelHint =
    destinationKind === "channel" &&
    !!activePlaybook?.prefersChannel &&
    !channelId;

  const applyPlaybook = (id: NoteWorkerPlaybookId) => {
    const playbook = noteWorkerPlaybookById(id);
    if (!playbook) return;
    const key = playbookInstructionKey(id);
    dispatch({
      type: "applyPlaybook",
      playbookId: id,
      instruction: t(($) => $.notes_page[key]),
      prefersChannel: playbook.prefersChannel,
      agentId: preferredAgentId,
    });
  };

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
    dispatch({ type: "setSubmitting", submitting: true });
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
      dispatch({ type: "setSubmitting", submitting: false });
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
            <div className="text-sm font-medium">{t(($) => $.notes_page.worker_playbooks_label)}</div>
            <div className="flex flex-wrap gap-2">
              {NOTE_WORKER_PLAYBOOKS.map((playbook) => {
                const selected = playbookId === playbook.id;
                const labelKey = playbookLabelKey(playbook.id);
                return (
                  <button
                    key={playbook.id}
                    type="button"
                    className={cn(
                      "rounded-md border px-2.5 py-1 text-left text-xs",
                      selected
                        ? "border-primary/40 bg-muted font-medium text-foreground"
                        : "text-muted-foreground hover:bg-muted/50",
                    )}
                    onClick={() => applyPlaybook(playbook.id)}
                    disabled={submitting}
                  >
                    {t(($) => $.notes_page[labelKey])}
                  </button>
                );
              })}
            </div>
          </div>

          <div className="space-y-2">
            <div className="text-sm font-medium">{t(($) => $.notes_page.worker_destination_label)}</div>
            <div className="flex gap-1 rounded-md border p-1">
              <button
                type="button"
                className={cn(
                  "flex-1 rounded-md px-3 py-1.5 text-sm",
                  destinationKind === "agent" ? "bg-muted font-medium" : "text-muted-foreground hover:bg-muted/50",
                )}
                onClick={() => dispatch({ type: "setDestination", kind: "agent", agentId: preferredAgentId })}
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
                onClick={() => dispatch({ type: "setDestination", kind: "channel" })}
                disabled={submitting}
              >
                {t(($) => $.notes_page.worker_destination_channel)}
              </button>
            </div>
            {showChannelHint ? (
              <p className="text-xs text-muted-foreground">
                {t(($) => $.notes_page.worker_playbook_channel_hint)}
              </p>
            ) : null}
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
                        onClick={() => dispatch({ type: "setChannelId", channelId: channel.id })}
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
                      onClick={() => dispatch({ type: "setAgentId", agentId: agent.id })}
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
              onChange={(event) => dispatch({ type: "setInstruction", instruction: event.target.value })}
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
