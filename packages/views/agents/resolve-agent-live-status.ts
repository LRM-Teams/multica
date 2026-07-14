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
} from "./presence";

// One-to-one with the Activity timeline's dot tone → colour map (TONE_DOT in
// activity-timeline.tsx). The header projects the SAME latest Activity row, so
// the DOT uses the SAME type-based colour — command rows read amber, active work
// brand, etc. — never a separately-maintained stage palette.
const TONE_DOT_CLASS: Record<ActivityDotTone, string> = {
  neutral: "bg-muted-foreground/40",
  active: "bg-brand",
  running: "bg-[#F5B301]",
  waiting: "bg-warning",
  failure: "bg-destructive",
};

// The kind colour lives on the DOT only. The timeline paints every label —
// whatever the tone — in neutral foreground (activity-timeline.tsx renders the
// label as `text-foreground` for all rows). The header mirrors that row, so its
// label text is neutral too; the kind colour never bleeds onto the word. (Iris:
// "kind 色只上 dot、label 文字恒中性、header == 时间线同款" — a command label
// tinted amber, or Working tinted blue, would diverge from the very timeline the
// header is meant to match.)
const ACTIVITY_LABEL_TEXT = "text-foreground";

/**
 * Name-row live status for agent profile peeks.
 *
 * When the agent has an active task, project the latest Activity row verbatim
 * (Thinking / Running a command / …) — the SAME label the timeline shows, kind
 * colour on the dot only. Fall back to the coarse presence word (Idle / Offline
 * / …) when nothing is on the plate, so the header never says bare "Working"
 * while a tool is visibly running. Never an invented word — no "Queued": the
 * Activity timeline has no such row.
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
    // Working with a live Activity row → project it VERBATIM: the same word the
    // Activity timeline shows, with the kind colour on the dot only and the label
    // in neutral text (Parker: header = Activity latest-row projection; Iris:
    // kind 色只上 dot、文字恒中性). No separate chat-pill stage vocabulary or
    // stage palette — word and colour can never disagree with the timeline.
    if (latestActivity) {
      const p = activityPresentation(latestActivity);
      const rawLabel = tAgents(($) => $.tab_body.activity.labels[p.labelKey]);
      // Match the timeline's in-progress "…" on an active tool row.
      const label =
        latestActivity.activity_kind === "tool_call" && p.tone === "active"
          ? `${rawLabel}…`
          : rawLabel;
      return {
        label,
        textClass: ACTIVITY_LABEL_TEXT,
        dotClass: TONE_DOT_CLASS[p.tone],
      };
    }
    // Task on the plate but no Activity row streamed yet → read Thinking, the
    // neutral word the timeline opens a round with (activityPresentation maps
    // the thinking kind to the neutral tone). NOT "Queued": the timeline has no
    // queued row, so the header must never invent one from the task's snapshot
    // status (Frank: header = Activity projection only, Activity 里没有 queued).
    return {
      label: tAgents(($) => $.tab_body.activity.labels.thinking),
      textClass: ACTIVITY_LABEL_TEXT,
      dotClass: TONE_DOT_CLASS.neutral,
    };
  }

  // Idle plate — #288 presence word (Idle / Offline / Unstable / Archived).
  const label = formatPresenceStatus(presence, tAgents);
  const visual = presenceStatusVisual(presence);
  const dotClass = presenceStatusDotClass(presence);
  if (!label || !visual || !dotClass) return null;
  return { label, textClass: visual.textClass, dotClass };
}
