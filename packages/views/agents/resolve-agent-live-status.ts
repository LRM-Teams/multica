import type { TFunction } from "i18next";
import type { AgentPresenceDetail } from "@multica/core/agents";
import type { AgentTask, TaskMessagePayload } from "@multica/core/types";
import { pickStageKeys } from "../chat/components/task-status-pill";
import {
  availabilityConfig,
  formatPresenceStatus,
  presenceStatusDotClass,
  presenceStatusVisual,
  workloadConfig,
} from "./presence";

/**
 * Name-row live status for agent profile peeks.
 *
 * Prefer the chat-style stage word (Thinking / Running a command / Queued)
 * when the agent has an active task and a live task-message stream. Fall
 * back to the coarse presence word (Idle / Offline / …) only when nothing
 * is on the plate — so the header never says bare "Working" while a tool
 * is visibly running.
 */
export type AgentLiveStatusView = {
  label: string;
  textClass: string;
  dotClass: string;
};

/** Active-task priority: running first, then starting, hold, queued. */
const ACTIVE_STATUS_RANK: Record<string, number> = {
  running: 0,
  dispatched: 1,
  waiting_local_directory: 2,
  queued: 3,
};

/**
 * Pick the single most relevant active task for an agent from the workspace
 * snapshot. Terminal rows are ignored (snapshot includes one terminal per
 * agent for last-activity, not current workload).
 */
export function pickPrimaryActiveTask(
  snapshot: readonly AgentTask[],
  agentId: string | null | undefined,
): AgentTask | null {
  if (!agentId) return null;
  let best: AgentTask | null = null;
  let bestRank = Number.POSITIVE_INFINITY;
  for (const task of snapshot) {
    if (task.agent_id !== agentId) continue;
    const rank = ACTIVE_STATUS_RANK[task.status];
    if (rank === undefined) continue;
    if (rank < bestRank) {
      best = task;
      bestRank = rank;
      continue;
    }
    if (rank === bestRank && best) {
      const taskTime = task.started_at ?? task.dispatched_at ?? task.created_at;
      const bestTime = best.started_at ?? best.dispatched_at ?? best.created_at;
      if (taskTime > bestTime) best = task;
    }
  }
  return best;
}

export function resolveAgentLiveStatus(args: {
  presence: AgentPresenceDetail | "loading" | null | undefined;
  activeTask: AgentTask | null;
  taskMessages: readonly TaskMessagePayload[];
  tAgents: TFunction<"agents">;
  tChat: TFunction<"chat">;
}): AgentLiveStatusView | null {
  const { presence, activeTask, taskMessages, tAgents, tChat } = args;
  if (!presence || presence === "loading") return null;

  if (activeTask) {
    // Mirror TaskStatusPill: any streamed message means the daemon has
    // started work, even if the snapshot status is still lagging.
    const effectiveStatus =
      taskMessages.length > 0 ? "running" : activeTask.status;
    const availability =
      presence.availability === "archived"
        ? "offline"
        : presence.availability;
    const decision = pickStageKeys(
      effectiveStatus,
      taskMessages,
      availability,
    );
    const label = decision.toolKey
      ? tChat(($) => $.status_pill.tools[decision.toolKey!])
      : tChat(($) => $.status_pill.stages[decision.stageKey]);
    return {
      label,
      textClass: stageTextClass(decision.stageKey, !!decision.toolKey),
      dotClass: stageDotClass(decision.stageKey, !!decision.toolKey),
    };
  }

  // Idle plate — #288 presence word (Idle / Offline / Unstable / Archived).
  const label = formatPresenceStatus(presence, tAgents);
  const visual = presenceStatusVisual(presence);
  const dotClass = presenceStatusDotClass(presence);
  if (!label || !visual || !dotClass) return null;
  return {
    label,
    textClass: visual.textClass,
    dotClass,
  };
}

function stageTextClass(stageKey: string, hasTool: boolean): string {
  if (stageKey === "offline") return availabilityConfig.offline.textClass;
  if (stageKey === "reconnecting") return availabilityConfig.unstable.textClass;
  if (stageKey === "queued" || stageKey === "waiting_local_directory") {
    return workloadConfig.queued.textClass;
  }
  // starting_up / thinking / typing / tool_use — active work.
  if (hasTool || stageKey === "thinking" || stageKey === "typing" || stageKey === "starting_up") {
    return workloadConfig.working.textClass;
  }
  return workloadConfig.working.textClass;
}

function stageDotClass(stageKey: string, hasTool: boolean): string {
  if (stageKey === "offline") return availabilityConfig.offline.dotClass;
  if (stageKey === "reconnecting") return availabilityConfig.unstable.dotClass;
  if (stageKey === "queued" || stageKey === "waiting_local_directory") {
    return "bg-warning";
  }
  if (hasTool || stageKey === "thinking" || stageKey === "typing" || stageKey === "starting_up") {
    return "bg-brand";
  }
  return "bg-brand";
}
