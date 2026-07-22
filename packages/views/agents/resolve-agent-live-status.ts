import type { TFunction } from "i18next";
import type { AgentPresenceDetail } from "@multica/core/agents";
import type { AgentTask } from "@multica/core/types";
import {
  ACTIVITY_LABEL_EN,
  ACTIVITY_TONE_DOT_CLASS,
  activityPresentation,
  type ActivityEvent,
} from "./components/tabs/activity-event";
import {
  availabilityConfig,
  formatPresenceStatus,
  toLivePresence,
} from "./presence";

const TONE_DOT_CLASS = ACTIVITY_TONE_DOT_CLASS;
const ACTIVITY_LABEL_TEXT = "text-foreground";

/**
 * Live presence word for profile / name-row surfaces (LRM-248).
 * Only Online / Offline — never Unstable / Reconnecting / Idle / Working / Queued.
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

/**
 * Live Online/Offline word for profile cards and name rows (LRM-248).
 * Archived agents return null — avatar is grayscale with no live chrome.
 */
export function resolveAgentLiveStatus(args: {
  presence: AgentPresenceDetail | "loading" | null | undefined;
  /** @deprecated Ignored — live status is presence-only (LRM-248). */
  activeTask?: AgentTask | null;
  /** @deprecated Ignored — live status is presence-only (LRM-248). */
  latestActivity?: ActivityEvent | null;
  tAgents: TFunction<"agents">;
  /** @deprecated Ignored — live status is presence-only (LRM-248). */
  tChat?: TFunction<"chat">;
}): AgentLiveStatusView | null {
  const { presence, tAgents } = args;
  if (!presence || presence === "loading") return null;
  const live = toLivePresence(presence.availability);
  if (live === "archived") return null;

  const label = formatPresenceStatus(presence, tAgents);
  if (!label) return null;
  const visual =
    live === "online" ? availabilityConfig.online : availabilityConfig.offline;
  return { label, textClass: visual.textClass, dotClass: visual.dotClass };
}

/**
 * Composer / Activity header projection of the latest work row (Thinking /
 * Running command…). Not a live presence word — Activity event vocabulary is
 * allowed here (LRM-248). Never surfaces Unstable / Reconnecting.
 */
export function resolveAgentActivityHeader(args: {
  presence: AgentPresenceDetail | "loading" | null | undefined;
  activeTask: AgentTask | null;
  latestActivity: ActivityEvent | null;
}): AgentLiveStatusView | null {
  const { presence, activeTask, latestActivity } = args;
  if (!presence || presence === "loading" || !activeTask) return null;
  const live = toLivePresence(presence.availability);
  if (live === "archived" || live === "offline") return null;

  if (latestActivity) {
    const p = activityPresentation(latestActivity);
    const rawLabel = ACTIVITY_LABEL_EN[p.labelKey];
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
  return {
    label: ACTIVITY_LABEL_EN.thinking,
    textClass: ACTIVITY_LABEL_TEXT,
    dotClass: TONE_DOT_CLASS.neutral,
  };
}
