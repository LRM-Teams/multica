import type { TFunction } from "i18next";
import type { AgentPresenceDetail } from "@multica/core/agents";
import type { AgentTask } from "@multica/core/types";
import {
  activityPresentation,
  type ActivityDotTone,
  type ActivityEvent,
} from "./components/tabs/activity-event";
import {
  availabilityConfig,
  formatPresenceStatus,
  presenceStatusDotClass,
  presenceStatusVisual,
  workloadConfig,
} from "./presence";

// One-to-one with the Activity timeline's dot tone → colour map (TONE_DOT in
// activity-timeline.tsx). The header projects the SAME latest Activity row, so it
// must use the SAME type-based colours — command rows read amber, active work
// brand, etc. — never a separately-maintained stage palette (Parker: "字和点色
// 一起投影，复用行的 type 色表").
const TONE_DOT_CLASS: Record<ActivityDotTone, string> = {
  neutral: "bg-muted-foreground/40",
  active: "bg-brand",
  running: "bg-[#F5B301]",
  waiting: "bg-warning",
  failure: "bg-destructive",
};

function toneTextClass(tone: ActivityDotTone): string {
  if (tone === "waiting") return workloadConfig.queued.textClass;
  if (tone === "failure") return availabilityConfig.offline.textClass;
  if (tone === "neutral") return workloadConfig.idle.textClass;
  return workloadConfig.working.textClass; // active / running
}

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
  latestActivity: ActivityEvent | null;
  tAgents: TFunction<"agents">;
  tChat: TFunction<"chat">;
}): AgentLiveStatusView | null {
  const { presence, activeTask, latestActivity, tAgents, tChat } = args;
  if (!presence || presence === "loading") return null;
  const availability =
    presence.availability === "archived" ? "offline" : presence.availability;

  if (activeTask) {
    // Connection state wins over any (possibly stale) activity row: a task queued
    // on an offline/unstable runtime reads Offline / Unstable, not a projected
    // stage.
    if (availability === "offline") {
      return {
        label: tChat(($) => $.status_pill.stages.offline),
        textClass: availabilityConfig.offline.textClass,
        dotClass: availabilityConfig.offline.dotClass,
      };
    }
    if (availability === "unstable") {
      return {
        label: tChat(($) => $.status_pill.stages.reconnecting),
        textClass: availabilityConfig.unstable.textClass,
        dotClass: availabilityConfig.unstable.dotClass,
      };
    }
    // Working with a live Activity row → project it VERBATIM: the same word and
    // the same type-based dot colour the Activity timeline shows (Parker: header
    // = Activity latest-row projection, reuse the row's type colour table). No
    // separate chat-pill stage vocabulary — word and colour can never disagree.
    if (latestActivity) {
      const p = activityPresentation(latestActivity);
      const rawLabel = tAgents(($) => $.tab_body.activity.labels[p.labelKey]);
      // Match the timeline's in-progress "…" on an active tool row.
      const label =
        latestActivity.activity_kind === "tool_call" && p.tone === "active"
          ? `${rawLabel}…`
          : rawLabel;
      return { label, textClass: toneTextClass(p.tone), dotClass: TONE_DOT_CLASS[p.tone] };
    }
    // Task on the plate but nothing streamed yet: a queued task reads Queued; a
    // running/starting one reads Thinking (it's working, just hasn't emitted a
    // row yet) — the same word the timeline opens a round with.
    if (
      activeTask.status === "queued" ||
      activeTask.status === "waiting_local_directory"
    ) {
      return {
        label: tChat(($) => $.status_pill.stages.queued),
        textClass: workloadConfig.queued.textClass,
        dotClass: "bg-warning",
      };
    }
    return {
      label: tAgents(($) => $.tab_body.activity.labels.thinking),
      textClass: workloadConfig.working.textClass,
      dotClass: "bg-brand",
    };
  }

  // Idle plate — #288 presence word (Idle / Offline / Unstable / Archived).
  const label = formatPresenceStatus(presence, tAgents);
  const visual = presenceStatusVisual(presence);
  const dotClass = presenceStatusDotClass(presence);
  if (!label || !visual || !dotClass) return null;
  return { label, textClass: visual.textClass, dotClass };
}
