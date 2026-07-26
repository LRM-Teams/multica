"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  agentTaskSnapshotOptions,
  useAgentPresenceDetail,
  type AgentPresenceDetail,
} from "@multica/core/agents";
import type { AgentTask } from "@multica/core/types";
import { UnicodeSpinner } from "@multica/ui/components/common/unicode-spinner";
import { cn } from "@multica/ui/lib/utils";
import { useWorkspaceId } from "@multica/core/hooks";
import { toLiveAvailability } from "../../agents/presence";
import {
  pickPrimaryActiveTask,
} from "../../agents/resolve-agent-live-status";
import { useAgentActivityEvents } from "../../agents/components/tabs/use-agent-activity-events";
import type { ActivityEvent } from "../../agents/components/tabs/activity-event";
import { friendlyBubbleToolLabel } from "../../chat/lib/bubble-cursor-activity";
import { useT } from "../../i18n/use-t";

const ACTIVITY_STAGE_KINDS = new Set(["thinking", "tool_call"]);

/**
 * Short bubble-style working label for agent DM header.
 * Examples: 思考中 / Edit / Shell — never path/command details or Working list.
 */
export function resolveDmShortWorkingLabel(args: {
  presence: AgentPresenceDetail | "loading" | null | undefined;
  activeTask: AgentTask | null;
  latestActivity: ActivityEvent | null;
  thinkingLabel: string;
  queuedLabel: string;
  startingLabel: string;
}): string | null {
  const { presence, activeTask, latestActivity, thinkingLabel, queuedLabel, startingLabel } =
    args;
  if (!presence || presence === "loading") return null;
  if (!activeTask) return null;

  const live = toLiveAvailability(presence.availability);
  if (live === null || live === "offline") return null;

  if (latestActivity) {
    if (latestActivity.activity_kind === "tool_call") {
      return friendlyBubbleToolLabel(latestActivity.tool);
    }
    if (latestActivity.activity_kind === "thinking") {
      return thinkingLabel;
    }
  }

  if (activeTask.status === "queued") return queuedLabel;
  if (activeTask.status === "dispatched") return startingLabel;
  if (activeTask.status === "waiting_local_directory") return startingLabel;
  return thinkingLabel;
}

function useDmShortWorkingLabel(agentId: string): string | null {
  const { t } = useT("chat");
  const wsId = useWorkspaceId();
  const presence = useAgentPresenceDetail(wsId, agentId);
  const { data: snapshot = [] } = useQuery({
    ...agentTaskSnapshotOptions(wsId ?? ""),
    enabled: !!wsId && !!agentId,
  });
  const activeTask = useMemo(
    () => pickPrimaryActiveTask(snapshot, agentId),
    [snapshot, agentId],
  );
  const { events } = useAgentActivityEvents(activeTask ? agentId : "");
  const roundStart = activeTask?.started_at ?? activeTask?.dispatched_at ?? null;
  const latestActivity = useMemo(() => {
    if (!roundStart) return null;
    let latest: ActivityEvent | null = null;
    for (const e of events) {
      if (e.occurred_at >= roundStart && ACTIVITY_STAGE_KINDS.has(e.activity_kind)) {
        latest = e;
      }
    }
    return latest;
  }, [events, roundStart]);

  return useMemo(
    () =>
      resolveDmShortWorkingLabel({
        presence,
        activeTask,
        latestActivity,
        thinkingLabel: t(($) => $.status_pill.stages.thinking),
        queuedLabel: t(($) => $.status_pill.stages.queued),
        startingLabel: t(($) => $.status_pill.stages.starting_up),
      }),
    [presence, activeTask, latestActivity, t],
  );
}

/**
 * Compact DM-header working cue: breathe spinner + short stage/tool label.
 * Intentionally not the old Working list / hover chrome (LRM-594); just the
 * same short verbs users already see in the bubble process fold.
 */
export function DmAgentWorkingCue({
  agentId,
  className,
}: {
  agentId: string;
  className?: string;
}) {
  const label = useDmShortWorkingLabel(agentId);
  if (!label) return null;

  return (
    <span
      className={cn(
        "inline-flex min-h-8 max-w-[9rem] items-center gap-1.5 text-xs text-muted-foreground",
        className,
      )}
      data-testid="dm-agent-working-cue"
      aria-live="polite"
    >
      <UnicodeSpinner name="breathe" className="opacity-70" />
      <span className="min-w-0 truncate animate-chat-text-shimmer font-medium">
        {label}
      </span>
    </span>
  );
}
