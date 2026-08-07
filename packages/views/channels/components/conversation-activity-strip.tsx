"use client";

import { useMemo } from "react";
import { useQueries } from "@tanstack/react-query";
import { useWorkspaceId } from "@multica/core/hooks";
import { runnerActivityOptions } from "@multica/core/agents/queries";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import { isCompactActivityLabel } from "./is-compact-activity-label";

export interface ConversationTypingActor {
  actorName: string;
}

export interface ConversationActivityAgent {
  id: string;
  displayName: string;
}

interface ActivityEntry {
  id: string;
  displayName: string;
  label: string;
  tone: string;
}

const TONE_DOT_CLASS: Record<string, string> = {
  neutral: "bg-muted-foreground/40",
  info: "bg-blue-500",
  warning: "bg-amber-500",
  error: "bg-destructive",
  success: "bg-success",
};

/**
 * Bottom composer / reply strip shared by group channels, thread replies, and
 * DMs. Humans keep the transient typing pulse; agents project their server-
 * owned runner activity so the user can see who is editing / searching /
 * thinking right now.
 */
export function ConversationActivityStrip({
  typingActors = [],
  workingAgents = [],
}: {
  typingActors?: readonly ConversationTypingActor[];
  workingAgents?: readonly ConversationActivityAgent[];
}) {
  const { t } = useT("channels");
  const wsId = useWorkspaceId();
  const typingNames = useMemo(
    () =>
      typingActors.flatMap((actor) => {
        const name = actor.actorName.trim();
        return name ? [name] : [];
      }),
    [typingActors],
  );

  const agentQueries = useQueries({
    queries: workingAgents.map((agent) => ({
      ...runnerActivityOptions(wsId ?? "", agent.id),
      enabled: !!wsId && !!agent.id,
      refetchInterval: 5000,
      refetchOnWindowFocus: true,
    })),
  });

  const agentEntries = useMemo<ActivityEntry[]>(() => {
    return agentQueries.flatMap((query, index) => {
      const agent = workingAgents[index];
      const summary = query.data?.summary;
      if (!agent || !summary) return [];
      if (summary.visibility !== "visible") return [];
      if (!isCompactActivityLabel(summary.label)) return [];
      if (summary.tone === "success" || summary.tone === "neutral") return [];
      return [
        {
          id: agent.id,
          displayName: agent.displayName,
          label: summary.label,
          tone: summary.tone,
        },
      ];
    });
  }, [agentQueries, workingAgents]);

  const typingLabel =
    typingNames.length === 0
      ? null
      : typingNames.length === 1
        ? t(($) => $.typing.single, { name: typingNames[0]! })
        : typingNames.length === 2
          ? t(($) => $.typing.pair, { a: typingNames[0]!, b: typingNames[1]! })
          : t(($) => $.typing.overflow, {
              a: typingNames[0]!,
              b: typingNames[1]!,
              count: typingNames.length,
            });

  if (!typingLabel && agentEntries.length === 0) return null;

  return (
    <div
      className="flex min-h-6 flex-col gap-1 px-5 pb-2 text-xs text-muted-foreground"
      aria-live="polite"
      data-testid="conversation-activity-strip"
    >
      {typingLabel ? (
        <span
          className="flex min-w-0 items-center gap-1 truncate"
          data-testid="conversation-typing-row"
        >
          <span className="truncate">{typingLabel}</span>
          <TypingDots />
        </span>
      ) : null}
      {agentEntries.length > 0 ? (
        <div
          className="flex min-w-0 flex-col gap-1"
          data-testid="conversation-agent-activity-row"
        >
          {agentEntries.slice(0, 2).map((entry) => (
            <span key={entry.id} className="flex min-w-0 items-center gap-1.5 truncate">
              <span
                aria-hidden="true"
                className={cn(
                  "size-1.5 shrink-0 rounded-full",
                  TONE_DOT_CLASS[entry.tone] ?? "bg-muted-foreground",
                )}
              />
              <span className="truncate font-medium text-foreground">{entry.displayName}</span>
              <span className="truncate text-muted-foreground">{entry.label}</span>
            </span>
          ))}
          {agentEntries.length > 2 ? (
            <span className="truncate text-muted-foreground">
              {t(($) => $.conversation_activity.more_agents, {
                count: agentEntries.length - 2,
              })}
            </span>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

function TypingDots() {
  return (
    <span className="flex shrink-0 items-end gap-0.5" aria-hidden="true">
      <span className="size-1 animate-pulse rounded-full bg-muted-foreground/60 [animation-delay:-0.24s]" />
      <span className="size-1 animate-pulse rounded-full bg-muted-foreground/60 [animation-delay:-0.12s]" />
      <span className="size-1 animate-pulse rounded-full bg-muted-foreground/60" />
    </span>
  );
}
